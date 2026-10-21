package data

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/require"
)

// The same fixture also runs against the committed 3A source. It deliberately
// uses only the pre-3B public API so transaction timings are directly comparable.
func TestMaintainConversationTree_LargeUnitsMySQL(t *testing.T) {
	dsn := os.Getenv("AGENTLY_TEST_CLEANUP_BENCHMARK_DSN")
	if dsn == "" {
		t.Skip("AGENTLY_TEST_CLEANUP_BENCHMARK_DSN is not set")
	}
	cfg, err := mysql.ParseDSN(dsn)
	require.NoError(t, err)
	require.Equal(t, "tcp", cfg.Net)
	require.Equal(t, "127.0.0.1:3308", cfg.Addr)
	require.Equal(t, "cleanup_bench_verify_local", cfg.DBName)
	db, err := sql.Open("mysql", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	svc := NewService(newNativeMySQLRuntime(t, dsn))
	tag := fmt.Sprintf("large-unit-%d", time.Now().UnixNano())
	trigger := fmt.Sprintf("cleanup_slow_unit_%d", time.Now().UnixNano())
	root, run := tag+"-root", tag+"-run"
	t.Cleanup(func() {
		_, err := db.Exec("DROP TRIGGER IF EXISTS " + trigger)
		require.NoError(t, err)
		_, err = db.Exec("DELETE FROM run WHERE id=?", run)
		require.NoError(t, err)
		_, err = db.Exec("DELETE FROM conversation WHERE id=?", root)
		require.NoError(t, err)
	})
	old := time.Now().UTC().Add(-60 * 24 * time.Hour).Truncate(time.Second)
	_, err = db.Exec(`INSERT INTO conversation(id,status,created_at,updated_at,last_activity) VALUES(?,'completed',?,?,?)`, root, old, old, old)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO run(id,conversation_id,run_kind,protocol_run_id,protocol_status,protocol_revision,protocol_last_sequence,status,created_at,updated_at) VALUES(?,?,'agui',?,'completed',1,1001,'completed',?,?)`, run, root, run, old, old)
	require.NoError(t, err)
	tx, err := db.Begin()
	require.NoError(t, err)
	defer tx.Rollback()
	for i := 0; i < 1001; i++ {
		_, err = tx.Exec(`INSERT INTO call_payload(id,kind,mime_type,size_bytes,storage,inline_body,compression,run_id,sequence,created_at) VALUES(?,'agui.event','application/json',2,'inline','{}','none',?,?,?)`, fmt.Sprintf("%s-event-%04d", tag, i), run, i+1, old)
		require.NoError(t, err)
	}
	require.NoError(t, tx.Commit())
	request := ConversationMaintenanceRequest{RootID: root, Kind: ConversationMaintenanceInteractive, InactiveBefore: old.Add(24 * time.Hour), Mode: ConversationMaintenanceDryRun}
	out, err := svc.MaintainConversationTree(context.Background(), request)
	require.NoError(t, err)
	require.True(t, out.Eligible)
	acquired, err := svc.AcquireMaintenanceLease(context.Background(), MaintenanceLeaseAcquireRequest{Key: tag + "-lease", OwnerID: tag, TTL: 5 * time.Minute})
	require.NoError(t, err)
	require.True(t, acquired.Acquired)
	request.Mode, request.Lease = ConversationMaintenanceDelete, acquired.Lease
	t.Cleanup(func() {
		_, err := svc.ReleaseMaintenanceLease(context.Background(), acquired.Lease)
		require.NoError(t, err)
	})
	_, err = db.Exec(fmt.Sprintf(`CREATE TRIGGER %s BEFORE DELETE ON call_payload FOR EACH ROW BEGIN IF OLD.run_id='%s' THEN DO SLEEP(0.005); END IF; END`, trigger, run))
	require.NoError(t, err)
	for _, query := range []string{
		`UPDATE performance_schema.setup_instruments SET ENABLED='YES', TIMED='YES' WHERE NAME='transaction'`,
		`UPDATE performance_schema.setup_consumers SET ENABLED='YES' WHERE NAME='events_transactions_history_long'`,
		`TRUNCATE performance_schema.events_transactions_history_long`,
	} {
		_, err = db.Exec(query)
		require.NoError(t, err)
	}
	started := time.Now()
	out, err = svc.MaintainConversationTree(context.Background(), request)
	elapsed := time.Since(started)
	require.NoError(t, err)
	require.True(t, out.Deleted)
	require.Equal(t, 1, out.ConversationCount)
	var count int
	var maximum, total float64
	require.NoError(t, db.QueryRow(`SELECT COUNT(*),COALESCE(MAX(TIMER_WAIT),0)/1000000000000,COALESCE(SUM(TIMER_WAIT),0)/1000000000000 FROM performance_schema.events_transactions_history_long WHERE STATE='COMMITTED'`).Scan(&count, &maximum, &total))
	require.Greater(t, count, 0)
	require.Greater(t, maximum, 1.0, "transaction instrumentation must observe deliberately slow DML")
	t.Logf("large-unit timing: events=1001 elapsed=%s committed_transactions=%d max_transaction_seconds=%.6f total_transaction_seconds=%.6f", elapsed, count, maximum, total)
	if os.Getenv("AGENTLY_TEST_CLEANUP_EXPECT_UNITS") != "false" {
		require.Less(t, maximum, elapsed.Seconds()*0.75, "no single transaction should retain all 1001 slow event deletes")
	}
	assertStage1RowCount(t, db, "conversation", "id", root, 0)
	assertStage1RowCount(t, db, "run", "id", run, 0)
	var residual int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM call_payload WHERE id LIKE ?`, tag+"-%").Scan(&residual))
	require.Zero(t, residual)
}
