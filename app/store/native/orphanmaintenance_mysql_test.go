package native_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/store/native"
	maintenance "github.com/viant/agently-core/internal/store/maintenancelease"
	orphan "github.com/viant/agently-core/internal/store/orphanmaintenance"
	"github.com/viant/datly/bootstrap/connector"
)

func orphanMySQLFixture(t *testing.T) (*orphan.Store, *maintenance.Store, *sql.DB, string) {
	t.Helper()
	dsn := os.Getenv("AGENTLY_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("AGENTLY_TEST_MYSQL_DSN is not set")
	}
	db, err := sql.Open("mysql", dsn)
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	_, err = db.Exec("SET FOREIGN_KEY_CHECKS=0")
	require.NoError(t, err)
	prefix := fmt.Sprintf("agently-migration-mysql-orphan-%d-", time.Now().UnixNano())
	t.Cleanup(func() {
		for _, item := range []struct{ table, key string }{
			{"conversation_report_context", "conversation_id"}, {"report_export_artifact", "artifact_id"}, {"report_export_job", "job_id"}, {"report_run", "report_run_id"},
			{"model_call", "message_id"}, {"tool_call", "message_id"}, {"generated_file", "id"}, {"tool_approval_queue", "id"}, {"tool_execution_claim", "claim_key"}, {"turn_queue", "id"},
			{"investigation", "id"}, {"schedule_run", "id"}, {"run", "id"}, {"goal", "id"}, {"message", "id"}, {"turn", "id"}, {"schedule", "id"}, {"conversation", "id"}, {"call_payload", "id"}, {"maintenance_lease", "lease_key"},
		} {
			_, cleanupErr := db.Exec("DELETE FROM "+item.table+" WHERE "+item.key+" LIKE ?", prefix+"%")
			require.NoError(t, cleanupErr)
		}
		_, restoreErr := db.Exec("SET FOREIGN_KEY_CHECKS=1")
		require.NoError(t, restoreErr)
	})
	_, file, _, _ := runtime.Caller(0)
	server, err := native.New(context.Background(), native.Options{SourceRoot: filepath.Join(filepath.Dir(file), "..", "..", ".."), Connectors: []connector.Config{{Name: "agently", Driver: "mysql", DSN: dsn}}})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Shutdown(context.Background())) })
	return &orphan.Store{Invoker: server}, &maintenance.Store{Invoker: server}, db, prefix
}

func TestWorkspaceRuntimeOrphanEveryMySQLRule(t *testing.T) {
	store, leases, db, prefix := orphanMySQLFixture(t)
	require.Len(t, orphan.Rules(true), 59)
	runEveryOrphanRule(t, store, leases, db, true, prefix)
}
func TestWorkspaceRuntimeOrphanMySQLFenceRollbackAndLockedRecheck(t *testing.T) {
	store, leases, db, prefix := orphanMySQLFixture(t)
	ctx := context.Background()
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cutoff := old.Add(24 * time.Hour)
	_, err := db.Exec("INSERT INTO conversation(id,created_at,status) VALUES(?,?,'succeeded')", prefix+"parent", old)
	require.NoError(t, err)
	messageID := prefix + "message"
	_, err = db.Exec("INSERT INTO message(id,conversation_id,linked_conversation_id,role,type,content,created_at,updated_at) VALUES(?, ?, ?, 'assistant','text','preserved',?,?)", messageID, prefix+"parent", prefix+"missing", old, old)
	require.NoError(t, err)
	key := prefix + "fence"
	stale := orphanLease(t, leases, key, prefix+"old-worker")
	released, err := leases.Release(ctx, stale)
	require.NoError(t, err)
	require.True(t, released)
	held := orphanLease(t, leases, key, prefix+"new-worker")
	request := orphan.Request{RuleID: "message.missing_linked_conversation", RecordID: messageID, OlderThan: cutoff, Lease: stale}
	_, err = store.Apply(ctx, request)
	require.ErrorIs(t, err, maintenance.ErrLeaseLost)
	request.Lease = held
	// Use a distinct older value so second-precision columns cannot mask a committed fence.
	_, err = db.Exec("UPDATE maintenance_lease SET updated_at=? WHERE lease_key=?", old, key)
	require.NoError(t, err)
	var before time.Time
	require.NoError(t, db.QueryRow("SELECT updated_at FROM maintenance_lease WHERE lease_key=?", key).Scan(&before))
	trigger := fmt.Sprintf("agently_orphan_fail_%d", time.Now().UnixNano())
	_, err = db.Exec("CREATE TRIGGER " + trigger + " BEFORE UPDATE ON message FOR EACH ROW BEGIN IF NEW.id='" + messageID + "' THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='orphan fixture late failure'; END IF; END")
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = db.Exec("DROP TRIGGER IF EXISTS " + trigger) })
	_, err = store.Apply(ctx, request)
	require.ErrorContains(t, err, "orphan fixture late failure")
	var after time.Time
	require.NoError(t, db.QueryRow("SELECT updated_at FROM maintenance_lease WHERE lease_key=?", key).Scan(&after))
	require.Equal(t, before, after, "lease fence must rollback together with mutation")
	var link, content string
	require.NoError(t, db.QueryRow("SELECT linked_conversation_id,content FROM message WHERE id=?", messageID).Scan(&link, &content))
	require.Equal(t, prefix+"missing", link)
	require.Equal(t, "preserved", content)
	_, err = db.Exec("DROP TRIGGER " + trigger)
	require.NoError(t, err)
	// Another transaction changes the deciding row before our native locked read.
	// Apply must wait and then classify the committed row as no longer eligible.
	external, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = external.Rollback() })
	_, err = external.ExecContext(ctx, "UPDATE message SET linked_conversation_id=NULL WHERE id=?", messageID)
	require.NoError(t, err)
	type outcome struct {
		result *orphan.Result
		err    error
	}
	done := make(chan outcome, 1)
	applyCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	go func() { result, applyErr := store.Apply(applyCtx, request); done <- outcome{result, applyErr} }()
	select {
	case <-done:
		t.Fatal("native cleanup completed before held deciding row was released")
	case <-time.After(100 * time.Millisecond):
	}
	require.NoError(t, external.Commit())
	select {
	case result := <-done:
		require.NoError(t, result.err)
		require.Equal(t, orphan.NoLongerEligible, result.result.Reason)
		require.False(t, result.result.Mutated)
	case <-time.After(10 * time.Second):
		t.Fatal("native locked recheck did not resume")
	}
}

func TestWorkspaceRuntimeOrphanMySQLBlankLegacyReferences(t *testing.T) {
	store, leases, db, prefix := orphanMySQLFixture(t)
	ctx := context.Background()
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	lease := orphanLease(t, leases, prefix+"blank-lease", prefix+"worker")
	for _, item := range []struct{ rule, table, key, id, statement string }{
		{"schedule_run.missing_schedule", "schedule_run", "id", prefix + "legacy", "INSERT INTO schedule_run(id,schedule_id,status,conversation_kind,created_at,updated_at) VALUES(?,'','failed','scheduled',?,?)"},
		{"tool_execution_claim.missing_turn", "tool_execution_claim", "claim_key", prefix + "claim", "INSERT INTO tool_execution_claim(claim_key,rule_id,canonical_tool_name,turn_id,semantic_request_hash,state,created_at,updated_at) VALUES(?,'rule','tool','','hash','failed',?,?)"},
		{"report_export_artifact.missing_job", "report_export_artifact", "artifact_id", prefix + "artifact", "INSERT INTO report_export_artifact(artifact_id,job_id,artifact_ref,owner_id,format,content_type,created_at,retention_ttl_sec) VALUES(?,'missing','ref','','pdf','application/pdf',?,0)"},
	} {
		t.Run(item.rule, func(t *testing.T) {
			args := []any{item.id, old, old}
			if item.table == "report_export_artifact" {
				args = args[:2]
			}
			_, err := db.Exec(item.statement, args...)
			require.NoError(t, err)
			result, err := store.Apply(ctx, orphan.Request{RuleID: item.rule, RecordID: item.id, OlderThan: old.Add(24 * time.Hour), Lease: lease})
			require.NoError(t, err)
			require.True(t, result.Deleted)
			var count int
			require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM "+item.table+" WHERE "+item.key+"=?", item.id).Scan(&count))
			require.Zero(t, count)
		})
	}
}
