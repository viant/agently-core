package maintenance

import (
	"context"
	"database/sql"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	coredata "github.com/viant/agently-core/app/store/data"
	"github.com/viant/agently-core/app/store/native"
	payloaddelete "github.com/viant/agently-core/internal/datly/payload/delete"
	"github.com/viant/agently-core/internal/datly/queryselectors"
	"github.com/viant/agently-core/internal/store/maintenancelease"
	"github.com/viant/agently-core/internal/store/maintenancetx"
	"github.com/viant/bindly/locator"
	"github.com/viant/datly/bootstrap/connector"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/runtime/handler/provider"
	"github.com/viant/datly/spec"
	xhandler "github.com/viant/xdatly/handler"
)

type cleanupStage2Runtime interface {
	dexec.ComponentInvoker
	ConfiguredDriver(context.Context, string) (string, error)
}

// Observe only the top-level boundary. Child reads/writes remain stock Datly.
type cleanupStage2Invoker struct {
	cleanupStage2Runtime
	before   func(context.Context) error
	path     string
	mu       sync.Mutex
	outcomes []xhandler.Outcome
}

func (i *cleanupStage2Invoker) InvokeComponent(ctx context.Context, r dexec.ComponentRequest) (any, error) {
	path := i.path
	if path == "" {
		path = "/v1/internal/agently/scheduled-run/maintenance"
	}
	if r.Target.Route.Path != path {
		return i.cleanupStage2Runtime.InvokeComponent(ctx, r)
	}
	if i.before != nil {
		if err := i.before(ctx); err != nil {
			return nil, err
		}
	}
	complete := r.Completion
	r.Completion = func(o xhandler.Outcome) {
		i.mu.Lock()
		i.outcomes = append(i.outcomes, o.Clone())
		i.mu.Unlock()
		if complete != nil {
			complete(o)
		}
	}
	return i.cleanupStage2Runtime.InvokeComponent(ctx, r)
}

func TestCleanupStage2MySQL(t *testing.T) {
	dsn := os.Getenv(cleanupHeartbeatDSNEnvironment)
	if dsn == "" {
		t.Skip("use script/mysql/repro_cleanup_heartbeat.sh")
	}
	cfg, err := mysql.ParseDSN(dsn)
	require.NoError(t, err)
	require.NoError(t, validateCleanupHeartbeatDSN(cfg))
	cfg.ParseTime, cfg.Loc = true, time.UTC
	if cfg.Params == nil {
		cfg.Params = map[string]string{}
	}
	cfg.Params["innodb_lock_wait_timeout"] = "15"
	tag := uuid.NewString()
	db, err := sql.Open("mysql", cfg.FormatDSN())
	require.NoError(t, err)
	db.SetMaxOpenConns(5)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	// Share the heartbeat reproduction's guard: both tests use the fixed worker
	// lease key. This advisory lock belongs only to this disposable test harness.
	guard, err := db.Conn(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, guard.Close()) })
	var locked int
	require.NoError(t, guard.QueryRowContext(t.Context(), "SELECT GET_LOCK('agently_cleanup_heartbeat_repro', 0)").Scan(&locked))
	require.Equal(t, 1, locked, "another reproduction is already running")
	t.Cleanup(func() {
		var released int
		require.NoError(t, guard.QueryRowContext(context.Background(), "SELECT RELEASE_LOCK('agently_cleanup_heartbeat_repro')").Scan(&released))
		require.Equal(t, 1, released)
	})
	for _, table := range []string{"conversation", "run", "schedule", "maintenance_lease"} {
		var count int
		require.NoError(t, db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM `"+table+"`").Scan(&count))
		require.Zero(t, count, "requires an empty dedicated reproduction schema: %s", table)
	}
	cfg.ConnectionAttributes = "cleanup_repro:" + tag
	server, err := native.New(t.Context(), native.Options{Connectors: []connector.Config{{Name: "agently", Driver: "mysql", DSN: cfg.FormatDSN(), MaxOpenConns: 8, MaxIdleConns: 4}}})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Shutdown(context.Background())) })
	base := coredata.NewService(server)
	t.Setenv("AGENTLY_DELETE_PAYLOAD_MODE", "bulk")
	acquire := func(t *testing.T, owner string) coredata.MaintenanceLease {
		r, err := base.AcquireMaintenanceLease(t.Context(), coredata.MaintenanceLeaseAcquireRequest{Key: conversationCleanupLeaseKey, OwnerID: owner, TTL: time.Minute})
		require.NoError(t, err)
		require.True(t, r.Acquired)
		return r.Lease
	}
	release := func(lease coredata.MaintenanceLease) {
		_, err := base.ReleaseMaintenanceLease(context.Background(), lease)
		require.NoError(t, err)
	}
	request := func(f *cleanupHeartbeatFixture, lease coredata.MaintenanceLease) coredata.ScheduledRunMaintenanceRequest {
		return coredata.ScheduledRunMaintenanceRequest{RunID: f.run, InactiveBefore: time.Now().Add(-30 * 24 * time.Hour), Mode: coredata.ConversationMaintenanceDelete, Lease: lease}
	}

	t.Run("takeover_overlapping_same_tree_and_SQL", func(t *testing.T) {
		f := seedCleanupHeartbeatFixture(t, db, true)
		leaseA := acquire(t, "stage2-a")
		f.owner = leaseA.OwnerID
		blocked, err := base.AcquireMaintenanceLease(t.Context(), coredata.MaintenanceLeaseAcquireRequest{Key: leaseA.Key, OwnerID: "stage2-b", TTL: time.Minute})
		require.NoError(t, err)
		require.False(t, blocked.Acquired, "normal second worker must skip")
		admitted, start := make(chan struct{}), make(chan struct{})
		var once sync.Once
		wrapped := &cleanupStage2Invoker{cleanupStage2Runtime: server, before: func(ctx context.Context) error {
			once.Do(func() { close(admitted) })
			select {
			case <-start:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}}
		serviceA := coredata.NewService(wrapped)
		ctx, cancel := context.WithTimeout(t.Context(), 40*time.Second)
		defer cancel()
		var a, b *coredata.ScheduledRunMaintenanceResult
		var errA, errB error
		doneA, doneB := make(chan struct{}), make(chan struct{})
		startedB := false
		// Capture only this disposable server; restore its original logging mode.
		var oldLog, oldOutput string
		require.NoError(t, db.QueryRow("SELECT @@global.general_log, @@global.log_output").Scan(&oldLog, &oldOutput))
		_, err = db.Exec("SET GLOBAL log_output='TABLE'")
		require.NoError(t, err)
		_, err = db.Exec("SET GLOBAL general_log=ON")
		require.NoError(t, err)
		t.Cleanup(func() {
			_, e := db.Exec("SET GLOBAL general_log=" + oldLog)
			require.NoError(t, e)
			_, e = db.Exec("SET GLOBAL log_output=?", oldOutput)
			require.NoError(t, e)
		})
		var since time.Time
		require.NoError(t, db.QueryRow("SELECT CURRENT_TIMESTAMP(6)").Scan(&since))
		// Deliberately inherited flags must disappear BEFORE any reader binding.
		ctx = queryselectors.ForUpdateOptions(ctx, true).Context(ctx)
		ctx = dexec.WithTransactionIsolation(ctx, dexec.IsolationSerializable)
		go func() { a, errA = serviceA.MaintainScheduledRun(ctx, request(f, leaseA)); close(doneA) }()
		t.Cleanup(func() {
			cancel()
			select {
			case <-doneA:
			case <-time.After(20 * time.Second):
				t.Error("first operation did not join")
			}
			if startedB {
				select {
				case <-doneB:
				case <-time.After(20 * time.Second):
					t.Error("second operation did not join")
				}
			}
			require.NoError(t, waitCleanupHeartbeatIdle(db, tag, ""))
			f.cleanupSafe = true
		})
		select {
		case <-admitted:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		_, err = db.ExecContext(ctx, "UPDATE maintenance_lease SET lease_until=CURRENT_TIMESTAMP - INTERVAL 1 SECOND WHERE lease_key=?", leaseA.Key)
		require.NoError(t, err)
		leaseB := acquire(t, "stage2-b")
		f.owner = leaseB.OwnerID
		defer release(leaseB)
		require.NotEqual(t, leaseA.Token, leaseB.Token)
		f.cleanupSafe = false
		close(start)
		var thread uint64
		require.NoError(t, pollCleanupHeartbeat(ctx, doneA, "first cleanup inside DELETE", func(ctx context.Context) (bool, error) {
			var e error
			thread, _, e = cleanupHeartbeatDeleteThread(ctx, db, cfg.DBName, tag)
			return thread != 0, e
		}))
		startedB = true
		go func() { b, errB = base.MaintainScheduledRun(ctx, request(f, leaseB)); close(doneB) }()
		require.NoError(t, pollCleanupHeartbeat(ctx, doneB, "second DELETE/UPDATE actually waiting for first cleanup", func(ctx context.Context) (bool, error) {
			var n int
			e := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM performance_schema.data_lock_waits w JOIN performance_schema.data_locks b ON b.ENGINE=w.ENGINE AND b.ENGINE_LOCK_ID=w.BLOCKING_ENGINE_LOCK_ID JOIN performance_schema.data_locks r ON r.ENGINE=w.ENGINE AND r.ENGINE_LOCK_ID=w.REQUESTING_ENGINE_LOCK_ID WHERE b.THREAD_ID=? AND r.OBJECT_SCHEMA=? AND r.OBJECT_NAME<>'maintenance_lease'`, thread, cfg.DBName).Scan(&n)
			return n > 0, e
		}))
		select {
		case <-doneA:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		select {
		case <-doneB:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		require.NoError(t, errA)
		require.NoError(t, errB)
		require.True(t, a.Deleted)
		require.Equal(t, 1, a.ConversationCount)
		require.False(t, b.Deleted)
		require.Zero(t, b.ConversationCount)
		f.assertRows(t, db, false)
		rows, err := db.QueryContext(ctx, `SELECT g.argument FROM mysql.general_log g JOIN performance_schema.session_connect_attrs a ON a.PROCESSLIST_ID=g.thread_id AND a.ATTR_NAME='cleanup_repro' AND a.ATTR_VALUE=? WHERE g.event_time>=? AND g.command_type IN ('Query','Prepare','Execute')`, tag, since)
		require.NoError(t, err)
		var statements, readCommitted int
		for rows.Next() {
			var query string
			require.NoError(t, rows.Scan(&query))
			query = strings.ToUpper(query)
			statements++
			require.NotContains(t, query, "FOR UPDATE")
			require.NotContains(t, query, "FOR SHARE")
			require.NotContains(t, query, "SERIALIZABLE")
			if strings.Contains(query, "READ COMMITTED") {
				readCommitted++
			}
		}
		require.NoError(t, rows.Err())
		require.NoError(t, rows.Close())
		require.Greater(t, statements, 20)
		require.GreaterOrEqual(t, readCommitted, 2)
		t.Logf("PROVEN takeover overlap: actual write wait; one tree deleted once; %d SQL statements, %d READ COMMITTED, no locking reads", statements, readCommitted)
	})

	t.Run("partial_payload_batch_keeps_new_reference_and_actual_count", func(t *testing.T) {
		f := seedCleanupHeartbeatFixture(t, db, false)
		lease := acquire(t, "stage2-payload")
		f.owner = lease.OwnerID
		defer release(lease)
		ids := []string{f.run + "-01-anchor", f.run + "-02-missing", f.run + "-03-reference", f.run + "-04-delete"}
		t.Cleanup(func() {
			if !f.cleanupSafe {
				return
			}
			_, err := db.Exec("DROP TRIGGER IF EXISTS `" + f.trigger + "`")
			require.NoError(t, err)
			_, err = db.Exec("DELETE FROM message WHERE conversation_id=?", f.control)
			require.NoError(t, err)
			for _, id := range ids {
				_, err = db.Exec("DELETE FROM call_payload WHERE id=?", id)
				require.NoError(t, err)
			}
		})
		for _, id := range ids {
			_, err := db.Exec("INSERT INTO call_payload(id,kind,mime_type,size_bytes,storage,inline_body,created_at) VALUES(?,'test','text/plain',7,'inline','payload',CURRENT_TIMESTAMP)", id)
			require.NoError(t, err)
		}
		// Only the first native anchor sleeps. The remaining guarded SQL must see
		// the disappearance and new external reference committed during that wait.
		_, err := db.Exec("CREATE TRIGGER `" + f.trigger + "` BEFORE DELETE ON call_payload FOR EACH ROW BEGIN IF OLD.id = '" + ids[0] + "' THEN DO SLEEP(10); END IF; END")
		require.NoError(t, err)
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		defer cancel()
		// Exercise the real private bulk writer with initially unreferenced IDs.
		// Attaching them to rows deleted by another open transaction would test
		// FK waiting instead of a late reference between pre-read and guarded DML.
		input := &payloaddelete.Input{}
		for _, id := range ids {
			row := &payloaddelete.PayloadDelete{}
			row.SetId(id)
			row.SetShouldDelete(true)
			input.Payloads = append(input.Payloads, row)
		}
		input.SetPayloads(input.Payloads)
		var value any
		var effects *maintenancetx.Effects
		var maintainErr error
		done := make(chan struct{})
		f.cleanupSafe = false
		go func() {
			value, effects, maintainErr = maintenancetx.Invoke(ctx, server, func(scope context.Context) (context.Context, error) {
				scope, err := (&maintenancelease.Store{Invoker: server}).Preflight(scope, maintenancelease.Lease{Key: lease.Key, OwnerID: lease.OwnerID, Token: lease.Token})
				if err != nil {
					return nil, err
				}
				return maintenancetx.Prepare(scope, server)
			}, dexec.ComponentRequest{
				Target: dexec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[payloaddelete.BulkDeleteComponent]().PkgPath(), Name: "PayloadBulkDelete"}, Route: spec.RouteRef{Method: "PATCH", Path: "/v1/internal/agently/payload/delete-bulk"}},
				Input:  input,
				Providers: []locator.Provider{provider.Named("payloadaccess", func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
					if name == "deleteUnreferenced" {
						return true, true, nil
					}
					return nil, false, nil
				})},
			})
			close(done)
		}()
		t.Cleanup(func() {
			cancel()
			select {
			case <-done:
			case <-time.After(20 * time.Second):
				t.Fatal("payload cleanup did not join; refusing fixture cleanup")
			}
			require.NoError(t, waitCleanupHeartbeatIdle(db, tag, ""))
			f.cleanupSafe = true
		})
		require.NoError(t, pollCleanupHeartbeat(ctx, done, "payload anchor sleeping after pre-read", func(ctx context.Context) (bool, error) {
			thread, _, err := cleanupHeartbeatDeleteThread(ctx, db, cfg.DBName, tag)
			return thread != 0, err
		}))
		_, err = db.ExecContext(ctx, "DELETE FROM call_payload WHERE id=?", ids[1])
		require.NoError(t, err)
		_, err = db.ExecContext(ctx, "INSERT INTO message(id,conversation_id,role,attachment_payload_id) VALUES(?,?,'assistant',?)", ids[2]+"-external", f.control, ids[2])
		require.NoError(t, err)
		select {
		case <-done:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		require.NoError(t, maintainErr)
		require.Empty(t, value.(*payloaddelete.Output).Data, "partial match must not invent deleted identities")
		// Without an enclosing maintenance component there is no root reporter;
		// this ledger holds the immediate SQL remainder, excluding its native
		// anchor. The combined native/remainder ledger is covered by unit tests.
		require.EqualValues(t, 1, effects.Deleted("call_payload"))
		for index, id := range ids {
			var n int
			require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM call_payload WHERE id=?", id).Scan(&n))
			if index == 2 {
				require.Equal(t, 1, n, "newly referenced payload must survive")
			} else {
				require.Zero(t, n)
			}
		}
		var referenced string
		require.NoError(t, db.QueryRow("SELECT attachment_payload_id FROM message WHERE id=?", ids[2]+"-external").Scan(&referenced))
		require.Equal(t, ids[2], referenced)
		f.assertRows(t, db, true)
		t.Log("PROVEN partial payload batch: disappeared row tolerated, new reference preserved, actual deletion count=2")
	})

	t.Run("disappeared_guarded_anchor_rolls_back_and_becomes_noop", func(t *testing.T) {
		lease := acquire(t, "stage2-anchor")
		defer release(lease)
		id := "stage2-anchor-" + uuid.NewString()
		_, err := db.Exec("INSERT INTO call_payload(id,kind,mime_type,size_bytes,storage,created_at) VALUES(?,'test','text/plain',0,'inline',CURRENT_TIMESTAMP - INTERVAL 60 DAY)", id)
		require.NoError(t, err)
		t.Cleanup(func() { _, err := db.Exec("DELETE FROM call_payload WHERE id=?", id); require.NoError(t, err) })
		tx, err := db.BeginTx(t.Context(), nil)
		require.NoError(t, err)
		defer tx.Rollback()
		_, err = tx.Exec("DELETE FROM call_payload WHERE id=?", id)
		require.NoError(t, err)
		wrapped := &cleanupStage2Invoker{cleanupStage2Runtime: server, path: "/v1/internal/agently/orphan-maintenance/apply"}
		service := coredata.NewService(wrapped)
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		defer cancel()
		done := make(chan struct{})
		var result *coredata.OrphanMaintenanceResult
		var maintainErr error
		go func() {
			result, maintainErr = service.MaintainOrphanCandidate(ctx, coredata.OrphanMaintenanceRequest{RuleID: "call_payload.unused", RecordID: id, OlderThan: time.Now().Add(-30 * 24 * time.Hour), Lease: lease})
			close(done)
		}()
		t.Cleanup(func() {
			cancel()
			_ = tx.Rollback()
			select {
			case <-done:
			case <-time.After(20 * time.Second):
				t.Fatal("anchor cleanup did not join")
			}
			require.NoError(t, waitCleanupHeartbeatIdle(db, tag, ""))
		})
		require.NoError(t, pollCleanupHeartbeat(ctx, done, "guarded anchor waiting for concurrent DELETE", func(ctx context.Context) (bool, error) {
			var n int
			err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM performance_schema.data_lock_waits w JOIN performance_schema.data_locks r ON r.ENGINE_LOCK_ID=w.REQUESTING_ENGINE_LOCK_ID WHERE r.OBJECT_SCHEMA=? AND r.OBJECT_NAME='call_payload'", cfg.DBName).Scan(&n)
			return n > 0, err
		}))
		require.NoError(t, tx.Commit())
		select {
		case <-done:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		require.NoError(t, maintainErr)
		require.False(t, result.Deleted)
		require.False(t, result.Mutated)
		require.Equal(t, coredata.OrphanMaintenanceNoLongerEligibleReason, result.Reason)
		require.Len(t, wrapped.outcomes, 2)
		require.Equal(t, xhandler.TransactionRolledBack, wrapped.outcomes[0].State())
		require.True(t, wrapped.outcomes[1].CommitConfirmed())
		t.Log("PROVEN disappeared guarded anchor: confirmed rollback, fresh lease/plan, no-op without false progress")
	})

	t.Run("real_deadlock_rolls_back_and_replans", func(t *testing.T) {
		f := seedCleanupHeartbeatFixture(t, db, false)
		lease := acquire(t, "stage2-deadlock")
		f.owner = lease.OwnerID
		defer release(lease)
		// Give the competing transaction more undo work so InnoDB selects the
		// real cleanup transaction as the deadlock victim deterministically.
		_, err := db.Exec("CREATE TABLE cleanup_stage2_deadlock (id INT PRIMARY KEY, value INT NOT NULL) ENGINE=InnoDB")
		require.NoError(t, err)
		t.Cleanup(func() { _, err := db.Exec("DROP TABLE cleanup_stage2_deadlock"); require.NoError(t, err) })
		for n := 0; n < 100; n++ {
			_, err := db.Exec("INSERT INTO cleanup_stage2_deadlock VALUES (?,0)", n)
			require.NoError(t, err)
		}
		_, err = db.Exec("CREATE TRIGGER `" + f.trigger + "` BEFORE DELETE ON message FOR EACH ROW UPDATE cleanup_stage2_deadlock SET value=value+1 WHERE id=0")
		require.NoError(t, err)
		t.Cleanup(func() { _, err := db.Exec("DROP TRIGGER IF EXISTS `" + f.trigger + "`"); require.NoError(t, err) })
		tx, err := db.BeginTx(t.Context(), nil)
		require.NoError(t, err)
		defer tx.Rollback()
		_, err = tx.Exec("UPDATE cleanup_stage2_deadlock SET value=value+1")
		require.NoError(t, err)
		ready := make(chan struct{})
		calls := 0
		wrapped := &cleanupStage2Invoker{cleanupStage2Runtime: server, before: func(ctx context.Context) error {
			calls++
			if calls > 1 {
				select {
				case <-ready:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			return nil
		}}
		service := coredata.NewService(wrapped)
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		defer cancel()
		done := make(chan struct{})
		var result *coredata.ScheduledRunMaintenanceResult
		var maintainErr error
		f.cleanupSafe = false
		go func() { result, maintainErr = service.MaintainScheduledRun(ctx, request(f, lease)); close(done) }()
		t.Cleanup(func() {
			cancel()
			_ = tx.Rollback()
			select {
			case <-done:
			case <-time.After(20 * time.Second):
				t.Error("deadlock cleanup did not join")
			}
			require.NoError(t, waitCleanupHeartbeatIdle(db, tag, ""))
			f.cleanupSafe = true
		})
		require.NoError(t, pollCleanupHeartbeat(ctx, done, "cleanup waiting on trigger row", func(ctx context.Context) (bool, error) {
			var n int
			e := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM performance_schema.data_lock_waits w JOIN performance_schema.data_locks r ON r.ENGINE_LOCK_ID=w.REQUESTING_ENGINE_LOCK_ID WHERE r.OBJECT_SCHEMA=? AND r.OBJECT_NAME='cleanup_stage2_deadlock'", cfg.DBName).Scan(&n)
			return n > 0, e
		}))
		_, err = tx.ExecContext(ctx, "UPDATE run SET status=status WHERE id=?", f.run)
		require.NoError(t, err, "cleanup, not heavier test transaction, must be deadlock victim")
		require.NoError(t, tx.Commit())
		close(ready)
		select {
		case <-done:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		require.NoError(t, maintainErr)
		require.True(t, result.Deleted)
		require.Equal(t, 1, result.ConversationCount)
		require.Equal(t, 2, calls, "one confirmed rollback and one fresh complete attempt")
		require.Len(t, wrapped.outcomes, 2)
		require.Equal(t, xhandler.TransactionRolledBack, wrapped.outcomes[0].State())
		require.True(t, wrapped.outcomes[1].CommitConfirmed())
		f.assertRows(t, db, false)
		t.Log("PROVEN real InnoDB deadlock: cleanup rollback, fresh lease/plan, successful second attempt")
	})
}
