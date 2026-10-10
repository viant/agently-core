package data

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/require"
	payloaddelete "github.com/viant/agently-core/internal/datly/payload/delete"
)

func TestDeleteConversationTree_PayloadBatchesRollbackSQLite(t *testing.T) {
	svc, db := newSeededServiceWithDB(t, func(t *testing.T, db *sql.DB) {
		_, err := db.Exec(`CREATE TABLE investigation (
            id TEXT PRIMARY KEY, title TEXT, created_by TEXT, conversation_id TEXT,
            summary TEXT, ad_order_id INTEGER, verdict TEXT,
            created DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`)
		require.NoError(t, err)
	})
	for _, mode := range []string{"row", "bulk"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv(payloaddelete.ModeEnvironment, mode)
			assertPayloadGraphBatchesRollback(t, svc, db, "sqlite")
		})
	}
}

func TestDeleteConversationTree_InvalidPayloadModeDoesNotMutate(t *testing.T) {
	t.Setenv(payloaddelete.ModeEnvironment, "invalid")
	svc, db := newSeededServiceWithDB(t, func(t *testing.T, db *sql.DB) {
		_, err := db.Exec(`INSERT INTO conversation(id,status,created_by_user_id) VALUES('invalid-mode-root','succeeded','u1')`)
		require.NoError(t, err)
	})
	require.ErrorContains(t, svc.DeleteConversationTree(deleteTestContext(), "invalid-mode-root"), payloaddelete.ModeEnvironment)
	var count int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM conversation WHERE id='invalid-mode-root'`).Scan(&count))
	require.Equal(t, 1, count)
}

// Only the already initialized, dedicated local benchmark database is permitted.
// The fault trigger is scoped to this test's tag and is removed before retry.
func TestDeleteConversationTree_PayloadBatchesRollbackMySQL(t *testing.T) {
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
	for _, mode := range []string{"row", "bulk"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv(payloaddelete.ModeEnvironment, mode)
			assertPayloadGraphBatchesRollback(t, svc, db, "mysql")
		})
	}
}

func assertPayloadGraphBatchesRollback(t *testing.T, svc Service, db *sql.DB, driver string) {
	t.Helper()
	tag := fmt.Sprintf("payload-graph-%d", time.Now().UnixNano())
	root, child, trigger := tag+"-root", tag+"-child", fmt.Sprintf("payload_batch_fail_%d", time.Now().UnixNano())
	t.Cleanup(func() {
		_, err := db.Exec("DROP TRIGGER IF EXISTS " + trigger)
		require.NoError(t, err)
		for _, statement := range []string{
			"DELETE FROM message WHERE id LIKE ?", "DELETE FROM turn WHERE id LIKE ?",
			"DELETE FROM investigation WHERE id LIKE ?", "DELETE FROM call_payload WHERE id LIKE ?",
			"DELETE FROM report_shared_artifact WHERE artifact_id LIKE ?",
			"UPDATE conversation SET conversation_parent_id=NULL WHERE id LIKE ?",
			"DELETE FROM conversation WHERE id LIKE ?",
		} {
			_, err := db.Exec(statement, tag+"-%")
			require.NoError(t, err)
		}
	})
	tx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	defer tx.Rollback()
	_, err = tx.Exec("INSERT INTO conversation(id,status,created_by_user_id) VALUES(?,'succeeded','u1')", root)
	require.NoError(t, err)
	_, err = tx.Exec("INSERT INTO conversation(id,status,created_by_user_id,conversation_parent_id) VALUES(?,'succeeded','u1',?)", child, root)
	require.NoError(t, err)
	for _, conv := range []string{root, child} {
		_, err = tx.Exec("INSERT INTO turn(id,conversation_id,status) VALUES(?,?,'succeeded')", conv+"-turn", conv)
		require.NoError(t, err)
	}
	_, err = tx.Exec("INSERT INTO investigation(id,conversation_id) VALUES(?,?)", tag+"-investigation", root)
	require.NoError(t, err)
	_, err = tx.Exec("INSERT INTO report_shared_artifact(artifact_id,artifact_ref,owner_id,kind,lifecycle) VALUES(?,'external://saved-report','u1','report','retained')", tag+"-report")
	require.NoError(t, err)
	for i := 0; i < 401; i++ {
		id := fmt.Sprintf("%s-payload-%03d", tag, i)
		body := []byte("body")
		if i == 0 || i == 400 {
			body = bytes.Repeat([]byte("x"), 1024*1024)
		}
		_, err = tx.Exec("INSERT INTO call_payload(id,kind,mime_type,size_bytes,storage,inline_body,compression) VALUES(?,'attachment','text/plain',?,'inline',?,'none')", id, len(body), body)
		require.NoError(t, err)
		conv := root
		if i >= 200 {
			conv = child
		}
		_, err = tx.Exec("INSERT INTO message(id,conversation_id,turn_id,role,type,attachment_payload_id) VALUES(?,?,?,'assistant','text',?)", fmt.Sprintf("%s-message-%03d", tag, i), conv, conv+"-turn", id)
		require.NoError(t, err)
	}
	require.NoError(t, tx.Commit())
	last := fmt.Sprintf("%s-payload-400", tag)
	// The failure text proves that the first 400 deletes really executed inside
	// this transaction. A premature commit would fail the rollback assertions.
	var triggerSQL string
	if driver == "mysql" {
		triggerSQL = fmt.Sprintf(`CREATE TRIGGER %s BEFORE DELETE ON call_payload FOR EACH ROW BEGIN
            IF OLD.id = '%s' THEN
                IF (SELECT COUNT(*) FROM call_payload WHERE id LIKE '%s-payload-%%' AND id < '%s') = 0 THEN
                    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'forced failure after first payload batch';
                ELSE
                    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'failure before first payload batch';
                END IF;
            END IF;
        END`, trigger, last, tag, last)
	} else {
		triggerSQL = fmt.Sprintf(`CREATE TRIGGER %s BEFORE DELETE ON call_payload WHEN OLD.id = '%s' BEGIN
            SELECT CASE WHEN (SELECT COUNT(*) FROM call_payload WHERE id LIKE '%s-payload-%%' AND id < '%s') = 0
                THEN RAISE(ABORT, 'forced failure after first payload batch')
                ELSE RAISE(ABORT, 'failure before first payload batch') END;
        END`, trigger, last, tag, last)
	}
	_, err = db.Exec(triggerSQL)
	require.NoError(t, err)
	err = svc.DeleteConversationTree(deleteTestContext(), root)
	require.ErrorContains(t, err, "forced failure after first payload batch")
	counts := func(deleted bool) {
		t.Helper()
		for _, entry := range []struct {
			table, key string
			before     int
		}{
			{"conversation", "id", 2}, {"turn", "id", 2}, {"message", "id", 401},
			{"call_payload", "id", 401}, {"investigation", "id", 1}, {"report_shared_artifact", "artifact_id", 1},
		} {
			want := entry.before
			if deleted && entry.table != "report_shared_artifact" {
				want = 0
			}
			var got int
			require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM "+entry.table+" WHERE "+entry.key+" LIKE ?", tag+"-%").Scan(&got))
			require.Equal(t, want, got, "%s after deletion/rollback", entry.table)
		}
	}
	counts(false)
	_, err = db.Exec("DROP TRIGGER " + trigger)
	require.NoError(t, err)
	require.NoError(t, svc.DeleteConversationTree(deleteTestContext(), root))
	counts(true)
	// Preserve the public API contract for an already removed root. Payload
	// deletion itself is idempotent; graph deletion still reports not found.
	require.ErrorIs(t, svc.DeleteConversationTree(deleteTestContext(), root), ErrConversationNotFound)
	counts(true)
}
