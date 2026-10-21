package maintenance

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	coredata "github.com/viant/agently-core/app/store/data"
	"github.com/viant/agently-core/app/store/native"
	"github.com/viant/datly/bootstrap/connector"
)

const cleanupHeartbeatDSNEnvironment = "AGENTLY_TEST_CLEANUP_HEARTBEAT_DSN"

// Regression for the original self-conflict: heartbeat must renew successfully
// while the real cleanup transaction is sleeping inside a DELETE trigger.
func TestCleanupHeartbeatSelfContentionMySQL(t *testing.T) {
	dsn := os.Getenv(cleanupHeartbeatDSNEnvironment)
	if dsn == "" {
		t.Skip(cleanupHeartbeatDSNEnvironment + " is not set; use script/mysql/repro_cleanup_heartbeat.sh")
	}
	cfg, err := mysql.ParseDSN(dsn)
	require.NoError(t, err)
	require.NoError(t, validateCleanupHeartbeatDSN(cfg))
	cfg.ParseTime, cfg.Loc, cfg.Timeout = true, time.UTC, 5*time.Second
	if cfg.Params == nil {
		cfg.Params = map[string]string{}
	}
	// DSN parameters apply to EVERY new connection, not one arbitrary pool session.
	cfg.Params["innodb_lock_wait_timeout"] = "3"
	tag := uuid.NewString()
	cfg.ConnectionAttributes = "cleanup_repro_observer:" + tag
	db, err := sql.Open("mysql", cfg.FormatDSN())
	require.NoError(t, err)
	db.SetMaxOpenConns(4)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	require.NoError(t, db.PingContext(ctx))
	var version string
	var performanceSchema int
	require.NoError(t, db.QueryRowContext(ctx, "SELECT VERSION(), @@performance_schema").Scan(&version, &performanceSchema))
	require.Equal(t, 1, performanceSchema, "performance_schema is required; absence of evidence must not PASS")
	t.Logf("MySQL %s; collision heartbeat=1100ms, lock_wait_timeout=3s, slow DELETE=10s, lease TTL=60s", version)
	// Preflight permissions and table/column availability before seeding anything.
	rows, err := db.QueryContext(ctx, cleanupHeartbeatLockWaitSQL, cfg.DBName, tag, tag)
	require.NoError(t, err, "performance_schema lock diagnostics are required")
	require.NoError(t, rows.Close())

	// Serialize independent invocations because the real worker uses a fixed key.
	guard, err := db.Conn(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = guard.Close() })
	var locked int
	require.NoError(t, guard.QueryRowContext(ctx, "SELECT GET_LOCK('agently_cleanup_heartbeat_repro', 0)").Scan(&locked))
	require.Equal(t, 1, locked, "another reproduction is already running")
	t.Cleanup(func() {
		releaseCtx, releaseCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer releaseCancel()
		var released int
		require.NoError(t, guard.QueryRowContext(releaseCtx, "SELECT RELEASE_LOCK('agently_cleanup_heartbeat_repro')").Scan(&released))
		require.Equal(t, 1, released)
	})
	for _, table := range []string{"conversation", "run", "schedule", "maintenance_lease"} {
		var count int
		require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM `"+table+"`").Scan(&count))
		require.Zero(t, count, "requires an empty dedicated reproduction schema: %s", table)
	}

	cfg.ConnectionAttributes = "cleanup_repro:" + tag
	server, err := native.New(t.Context(), native.Options{Connectors: []connector.Config{{
		Name: "agently", Driver: "mysql", DSN: cfg.FormatDSN(), MaxOpenConns: 8, MaxIdleConns: 4,
	}}})
	require.NoError(t, err)
	t.Cleanup(func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		require.NoError(t, server.Shutdown(shutdownCtx))
	})
	service := coredata.NewService(server)

	for _, scenario := range []struct {
		name      string
		slow      bool
		collision bool
		interval  time.Duration
	}{
		{name: "fast_delete_before_heartbeat", interval: 5 * time.Second},
		// The lease clock has second precision. Sub-second renewals can be a
		// no-op UPDATE (zero changed rows) rather than evidence of lock contention.
		{name: "slow_delete_fast_heartbeat", slow: true, collision: true, interval: 1100 * time.Millisecond},
		{name: "slow_delete_late_heartbeat", slow: true, interval: 30 * time.Second},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			fixture := seedCleanupHeartbeatFixture(t, db, scenario.slow)
			observed := &cleanupHeartbeatData{Data: service, maintainStarted: make(chan struct{}), renewed: make(chan struct{})}
			var gate chan struct{}
			if scenario.collision {
				gate = make(chan struct{})
				observed.renewGate = gate
			}
			worker := newConversationCleanupWorker(observed, Options{
				Enabled: true, BatchSize: 1, Timeout: 30 * time.Second, Debug: true,
			}, newScheduledRunCleanupPolicy(observed, 30*24*time.Hour, true))
			worker.leaseTTL, worker.leaseRenewInterval = time.Minute, scenario.interval
			fixture.owner = worker.leaseOwnerID
			fixture.cleanupSafe = false
			passCtx, passCancel := context.WithTimeout(t.Context(), 45*time.Second)
			finished := make(chan struct{})
			var result Result
			var passErr error
			var cleanupTransaction string
			started := time.Now()
			go func() {
				result, passErr = worker.RunPass(passCtx)
				t.Logf("worker returned: error=%v heartbeat_error=%v maintenance_error=%v", passErr, observed.renewErr, observed.maintainErr)
				close(finished)
			}()
			// Join the worker AND its server-side transaction before removing a trigger
			// or fixture. A canceled MySQL client can leave SLEEP running temporarily.
			t.Cleanup(func() {
				passCancel()
				select {
				case <-finished:
				case <-time.After(20 * time.Second):
					t.Fatal("worker did not stop; refusing to clean its active fixture")
				}
				require.NoError(t, waitCleanupHeartbeatIdle(db, tag, cleanupTransaction))
				fixture.cleanupSafe = true
			})

			if scenario.collision {
				barrierCtx, barrierCancel := context.WithTimeout(passCtx, 8*time.Second)
				defer barrierCancel()
				require.NoError(t, pollCleanupHeartbeat(barrierCtx, finished, "scheduled maintenance entry", func(context.Context) (bool, error) {
					select {
					case <-observed.maintainStarted:
						return true, nil
					default:
						return false, nil
					}
				}))
				var cleanupThread uint64
				require.NoError(t, pollCleanupHeartbeat(barrierCtx, finished, "slow DELETE trigger executing", func(ctx context.Context) (bool, error) {
					var err error
					cleanupThread, cleanupTransaction, err = cleanupHeartbeatDeleteThread(ctx, db, cfg.DBName, tag)
					return cleanupThread != 0, err
				}))
				close(gate) // First real Renew cannot race ahead of the slow DELETE.
				require.NoError(t, pollCleanupHeartbeat(barrierCtx, finished, "successful heartbeat during DELETE", func(context.Context) (bool, error) {
					select {
					case <-observed.renewed:
						return true, nil
					default:
						return false, nil
					}
				}))
				stillSleeping, _, err := cleanupHeartbeatDeleteThread(barrierCtx, db, cfg.DBName, tag)
				require.NoError(t, err)
				require.Equal(t, cleanupThread, stillSleeping, "renewal must complete before DELETE finishes")
				var leaseLocks int
				require.NoError(t, db.QueryRowContext(barrierCtx, `SELECT COUNT(*) FROM performance_schema.data_locks
                    WHERE OBJECT_SCHEMA=? AND OBJECT_NAME='maintenance_lease' AND THREAD_ID=?`, cfg.DBName, cleanupThread).Scan(&leaseLocks))
				require.Zero(t, leaseLocks, "cleanup transaction must not hold a lease-table lock")
				_, waiting, err := readCleanupHeartbeatLockWait(barrierCtx, db, cfg.DBName, tag)
				require.NoError(t, err)
				require.False(t, waiting, "heartbeat must not wait on cleanup's lease lock")
				t.Logf("PROVEN heartbeat renewed during slow DELETE: cleanup thread=%d trx=%s; no maintenance_lease locks held by cleanup", cleanupThread, cleanupTransaction)
			}

			select {
			case <-finished:
			case <-passCtx.Done():
				t.Fatal("worker did not finish within 45s")
			}
			require.NoError(t, waitCleanupHeartbeatIdle(db, tag, cleanupTransaction))
			t.Logf("pass duration=%s scanned=%d deleted=%d failed=%d renew_calls=%d error=%v", time.Since(started).Round(time.Millisecond), result.Scanned, result.Deleted, result.Failed, observed.renewCalls, passErr)
			require.False(t, result.LeaseSkipped)
			require.Equal(t, 1, result.Scanned)
			require.NoError(t, passErr)
			require.NoError(t, observed.renewErr)
			require.NoError(t, observed.maintainErr)
			require.Zero(t, result.Failed)
			require.Equal(t, 1, result.Deleted)
			fixture.assertRows(t, db, false)
			if scenario.collision {
				require.Greater(t, observed.renewCalls, 0)
			} else {
				require.Zero(t, observed.renewCalls, "successful controls must finish before their first heartbeat")
			}
			if scenario.slow {
				require.GreaterOrEqual(t, time.Since(started), 10*time.Second, "the slow trigger must actually execute")
			}
		})
	}
}

func validateCleanupHeartbeatDSN(cfg *mysql.Config) error {
	if cfg.Net != "tcp" || cfg.Addr != "127.0.0.1:3308" || cfg.DBName != "cleanup_heartbeat_repro_local" {
		return fmt.Errorf("cleanup heartbeat test requires exactly tcp(127.0.0.1:3308)/cleanup_heartbeat_repro_local")
	}
	if cfg.ConnectionAttributes != "" || cfg.MultiStatements {
		return fmt.Errorf("cleanup heartbeat test requires no connectionAttributes or multiStatements")
	}
	return nil
}

func TestCleanupHeartbeatDSNSafety(t *testing.T) {
	for _, dsn := range []string{
		"test@tcp(127.0.0.1:3308)/cleanup_heartbeat_repro_local",
		"test@tcp(127.0.0.1:3307)/cleanup_heartbeat_repro_local",
		"test@tcp(example.com:3308)/cleanup_heartbeat_repro_local",
		"test@tcp(127.0.0.1:3308)/agently",
		"test@tcp(127.0.0.1:3308)/agently_steward",
		"test@unix(/tmp/mysql.sock)/cleanup_heartbeat_repro_local",
		"test@tcp(127.0.0.1:3308)/cleanup_heartbeat_repro_local?multiStatements=true",
		"test@tcp(127.0.0.1:3308)/cleanup_heartbeat_repro_local?connectionAttributes=other:tag",
	} {
		t.Run(dsn, func(t *testing.T) {
			cfg, err := mysql.ParseDSN(dsn)
			require.NoError(t, err)
			if dsn == "test@tcp(127.0.0.1:3308)/cleanup_heartbeat_repro_local" {
				require.NoError(t, validateCleanupHeartbeatDSN(cfg))
			} else {
				require.Error(t, validateCleanupHeartbeatDSN(cfg))
			}
		})
	}
}

// A forwarding observer, not a fake: all mutations use the real data service.
// Fields are read only after RunPass has joined its heartbeat and returned.
type cleanupHeartbeatData struct {
	Data
	renewGate          <-chan struct{}
	maintainStarted    chan struct{}
	maintainOnce       sync.Once
	renewed            chan struct{}
	renewOnce          sync.Once
	renewCalls         int
	renewErr           error
	maintainErr        error
	maintainContextErr error
}

func (d *cleanupHeartbeatData) RenewMaintenanceLease(ctx context.Context, lease coredata.MaintenanceLease, ttl time.Duration) (*coredata.MaintenanceLeaseRenewResult, error) {
	if d.renewGate != nil {
		select {
		case <-d.renewGate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	d.renewCalls++
	result, err := d.Data.RenewMaintenanceLease(ctx, lease, ttl)
	if err != nil {
		d.renewErr = err
	}
	if err == nil && result != nil && result.Renewed {
		d.renewOnce.Do(func() { close(d.renewed) })
	}
	return result, err
}

func (d *cleanupHeartbeatData) MaintainScheduledRun(ctx context.Context, request coredata.ScheduledRunMaintenanceRequest) (*coredata.ScheduledRunMaintenanceResult, error) {
	d.maintainOnce.Do(func() { close(d.maintainStarted) })
	result, err := d.Data.MaintainScheduledRun(ctx, request)
	d.maintainErr, d.maintainContextErr = err, ctx.Err()
	return result, err
}

type cleanupHeartbeatFixture struct {
	schedule, run, conversation, turn, message, control, trigger, owner string
	cleanupSafe                                                         bool
}

func seedCleanupHeartbeatFixture(t *testing.T, db *sql.DB, slow bool) *cleanupHeartbeatFixture {
	t.Helper()
	tag := "heartbeat-repro-" + uuid.NewString()
	f := &cleanupHeartbeatFixture{schedule: tag + "-schedule", run: tag + "-run", conversation: tag + "-conversation", turn: tag + "-turn", message: tag + "-message", control: tag + "-control", trigger: "cleanup_heartbeat_" + strings.ReplaceAll(uuid.NewString(), "-", ""), cleanupSafe: true}
	t.Cleanup(func() {
		if !f.cleanupSafe {
			t.Error("refusing fixture cleanup: worker/server transaction has not been joined")
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_, err := db.ExecContext(ctx, "DROP TRIGGER IF EXISTS `"+f.trigger+"`")
		require.NoError(t, err)
		for _, statement := range []struct {
			sql  string
			args []any
		}{
			{"DELETE FROM run WHERE id=?", []any{f.run}},
			{"DELETE FROM message WHERE conversation_id=?", []any{f.conversation}},
			{"DELETE FROM turn WHERE id=?", []any{f.turn}},
			{"DELETE FROM conversation WHERE id IN (?,?)", []any{f.conversation, f.control}},
			{"DELETE FROM schedule WHERE id=?", []any{f.schedule}},
			{"DELETE FROM maintenance_lease WHERE lease_key=? AND owner_id=?", []any{conversationCleanupLeaseKey, f.owner}},
		} {
			_, err = db.ExecContext(ctx, statement.sql, statement.args...)
			require.NoError(t, err)
		}
	})
	old := time.Now().UTC().Add(-60 * 24 * time.Hour).Truncate(time.Second)
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO schedule(id,name,created_by_user_id,internal,visibility,agent_ref,enabled,schedule_type,timezone) VALUES(?,?,'repro-owner',0,'private','agent',1,'adhoc','UTC')`, []any{f.schedule, f.schedule}},
		{`INSERT INTO conversation(id,created_at,updated_at,last_activity,status,created_by_user_id) VALUES(?,?,?,?,'succeeded','repro-owner')`, []any{f.conversation, old, old, old}},
		{`INSERT INTO conversation(id,title,status) VALUES(?,'control must stay','succeeded')`, []any{f.control}},
		{`INSERT INTO run(id,schedule_id,conversation_id,conversation_kind,status,effective_user_id,created_at,updated_at,completed_at) VALUES(?,?,?,'scheduled','succeeded','repro-owner',?,?,?)`, []any{f.run, f.schedule, f.conversation, old, old, old}},
		{`INSERT INTO turn(id,conversation_id,status,created_at) VALUES(?,?,'succeeded',?)`, []any{f.turn, f.conversation, old}},
		{`INSERT INTO message(id,conversation_id,turn_id,role,content,created_at,updated_at) VALUES(?,?,?,'user','fixture message',?,?)`, []any{f.message, f.conversation, f.turn, old, old}},
		{`INSERT INTO message(id,conversation_id,turn_id,role,content,created_at,updated_at) VALUES(?,?,?,'assistant','fixture response',?,?)`, []any{f.message + "-reply", f.conversation, f.turn, old, old}},
	} {
		_, err := db.ExecContext(t.Context(), statement.sql, statement.args...)
		require.NoError(t, err)
	}
	if slow {
		// IDs contain only a fixed prefix and UUID; delay exactly one explicit
		// message DELETE, not cascades, unrelated rows or application instances.
		_, err := db.ExecContext(t.Context(), "CREATE TRIGGER `"+f.trigger+"` BEFORE DELETE ON message FOR EACH ROW BEGIN IF OLD.id = '"+f.message+"' THEN DO SLEEP(10); END IF; END")
		require.NoError(t, err)
	}
	return f
}

func (f *cleanupHeartbeatFixture) assertRows(t *testing.T, db *sql.DB, retained bool) {
	t.Helper()
	for _, check := range []struct {
		table, key string
		count      int
	}{
		{"run", f.run, 1}, {"conversation", f.conversation, 1}, {"turn", f.turn, 1},
		{"message", f.message, 1}, {"message", f.message + "-reply", 1},
	} {
		var count int
		require.NoError(t, db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM `"+check.table+"` WHERE id=?", check.key).Scan(&count))
		want := 0
		if retained {
			want = check.count
		}
		require.Equal(t, want, count, "row %s/%s (retained=%t)", check.table, check.key, retained)
	}
	if retained {
		var runConversation, runSchedule, runStatus string
		require.NoError(t, db.QueryRowContext(t.Context(), "SELECT conversation_id,schedule_id,status FROM run WHERE id=?", f.run).Scan(&runConversation, &runSchedule, &runStatus))
		require.Equal(t, f.conversation, runConversation, "rollback must restore detached references")
		require.Equal(t, f.schedule, runSchedule)
		require.Equal(t, "succeeded", runStatus)
		for _, message := range []struct{ id, content string }{{f.message, "fixture message"}, {f.message + "-reply", "fixture response"}} {
			var conversation, turn, content string
			require.NoError(t, db.QueryRowContext(t.Context(), "SELECT conversation_id,turn_id,content FROM message WHERE id=?", message.id).Scan(&conversation, &turn, &content))
			require.Equal(t, f.conversation, conversation)
			require.Equal(t, f.turn, turn)
			require.Equal(t, message.content, content)
		}
	}
	var title, scheduleName string
	var enabled int
	require.NoError(t, db.QueryRowContext(t.Context(), "SELECT title FROM conversation WHERE id=?", f.control).Scan(&title))
	require.Equal(t, "control must stay", title)
	require.NoError(t, db.QueryRowContext(t.Context(), "SELECT name,enabled FROM schedule WHERE id=?", f.schedule).Scan(&scheduleName, &enabled))
	require.Equal(t, f.schedule, scheduleName)
	require.Equal(t, 1, enabled)
}

const cleanupHeartbeatLockWaitSQL = `SELECT
    r.THREAD_ID, b.THREAD_ID, CAST(r.ENGINE_TRANSACTION_ID AS CHAR), CAST(b.ENGINE_TRANSACTION_ID AS CHAR),
    r.LOCK_MODE, b.LOCK_MODE, COALESCE(r.LOCK_DATA,''), COALESCE(bt.PROCESSLIST_INFO,'')
  FROM performance_schema.data_lock_waits w
  JOIN performance_schema.data_locks r ON r.ENGINE=w.ENGINE AND r.ENGINE_LOCK_ID=w.REQUESTING_ENGINE_LOCK_ID
  JOIN performance_schema.data_locks b ON b.ENGINE=w.ENGINE AND b.ENGINE_LOCK_ID=w.BLOCKING_ENGINE_LOCK_ID
  JOIN performance_schema.threads rt ON rt.THREAD_ID=r.THREAD_ID
  JOIN performance_schema.threads bt ON bt.THREAD_ID=b.THREAD_ID
  JOIN performance_schema.session_connect_attrs ra ON ra.PROCESSLIST_ID=rt.PROCESSLIST_ID AND ra.ATTR_NAME='cleanup_repro'
  JOIN performance_schema.session_connect_attrs ba ON ba.PROCESSLIST_ID=bt.PROCESSLIST_ID AND ba.ATTR_NAME='cleanup_repro'
  WHERE r.OBJECT_SCHEMA=? AND r.OBJECT_NAME='maintenance_lease' AND b.OBJECT_NAME='maintenance_lease'
    AND r.INDEX_NAME='PRIMARY' AND b.INDEX_NAME='PRIMARY' AND r.LOCK_TYPE='RECORD' AND b.LOCK_TYPE='RECORD'
    AND r.LOCK_STATUS='WAITING' AND b.LOCK_STATUS='GRANTED' AND ra.ATTR_VALUE=? AND ba.ATTR_VALUE=?`

type cleanupHeartbeatLockWait struct {
	RequestingThread, BlockingThread           uint64
	RequestingTransaction, BlockingTransaction string
	RequestingMode, BlockingMode, LockData     string
	BlockingSQL                                string
}

func readCleanupHeartbeatLockWait(ctx context.Context, db *sql.DB, schema, tag string) (cleanupHeartbeatLockWait, bool, error) {
	rows, err := db.QueryContext(ctx, cleanupHeartbeatLockWaitSQL, schema, tag, tag)
	if err != nil {
		return cleanupHeartbeatLockWait{}, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var result cleanupHeartbeatLockWait
		if err := rows.Scan(&result.RequestingThread, &result.BlockingThread, &result.RequestingTransaction, &result.BlockingTransaction, &result.RequestingMode, &result.BlockingMode, &result.LockData, &result.BlockingSQL); err != nil {
			return result, false, err
		}
		if strings.Contains(result.LockData, conversationCleanupLeaseKey) {
			return result, true, nil
		}
	}
	return cleanupHeartbeatLockWait{}, false, rows.Err()
}

func cleanupHeartbeatDeleteThread(ctx context.Context, db *sql.DB, schema, tag string) (uint64, string, error) {
	// Use live Performance Schema lock evidence rather than its join with the
	// cached information_schema.innodb_trx snapshot (which can lag under polling).
	rows, err := db.QueryContext(ctx, `SELECT t.THREAD_ID, CAST(l.ENGINE_TRANSACTION_ID AS CHAR)
      FROM performance_schema.threads t
      JOIN performance_schema.data_locks l ON l.THREAD_ID=t.THREAD_ID AND l.LOCK_TYPE='RECORD'
      JOIN performance_schema.session_connect_attrs a ON a.PROCESSLIST_ID=t.PROCESSLIST_ID
      WHERE t.PROCESSLIST_DB=? AND t.PROCESSLIST_STATE='User sleep'
        AND LOWER(COALESCE(t.PROCESSLIST_INFO,'')) LIKE '%sleep(10)%'
        AND a.ATTR_NAME='cleanup_repro' AND a.ATTR_VALUE=? LIMIT 1`, schema, tag)
	if err != nil {
		return 0, "", err
	}
	defer rows.Close()
	for rows.Next() {
		var thread uint64
		var transaction string
		if err := rows.Scan(&thread, &transaction); err != nil {
			return 0, "", err
		}
		return thread, transaction, nil
	}
	return 0, "", rows.Err()
}

func waitCleanupHeartbeatIdle(db *sql.DB, tag, cleanupTransaction string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return pollCleanupHeartbeat(ctx, nil, "server-side cleanup rollback/commit", func(ctx context.Context) (bool, error) {
		var count int
		// A disconnected session may lose its connection attributes before
		// rollback completes. Also track the actual captured blocking trx ID.
		err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.innodb_trx x
          LEFT JOIN performance_schema.threads t ON t.PROCESSLIST_ID=x.trx_mysql_thread_id
          LEFT JOIN performance_schema.session_connect_attrs a ON a.PROCESSLIST_ID=t.PROCESSLIST_ID AND a.ATTR_NAME='cleanup_repro'
          WHERE a.ATTR_VALUE=? OR x.trx_id=?`, tag, cleanupTransaction).Scan(&count)
		return count == 0, err
	})
}

func pollCleanupHeartbeat(ctx context.Context, finished <-chan struct{}, description string, check func(context.Context) (bool, error)) error {
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		ok, err := check(ctx)
		if err != nil {
			return fmt.Errorf("observe %s: %w", description, err)
		}
		if ok {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for %s: %w", description, ctx.Err())
		case <-finished:
			return fmt.Errorf("worker finished before observing %s", description)
		case <-ticker.C:
		}
	}
}
