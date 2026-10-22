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

// Exercise the real managed transaction, not a fake SQL provider. Even after
// the immediate event DELETE has executed, a later metadata failure must undo
// the entire small plan. Only an explicitly dedicated local database is allowed.
func TestMaintainConversationTree_SmallAGUIBulkMySQL(t *testing.T) {
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
	for _, n := range []int{0, 1, 399, 400} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			tag := fmt.Sprintf("small-agui-%d", time.Now().UnixNano())
			root, foreign := tag+"-root", tag+"-foreign"
			trigger := fmt.Sprintf("small_agui_fail_%d", time.Now().UnixNano())
			t.Cleanup(func() {
				_, err := db.Exec("DROP TRIGGER IF EXISTS " + trigger)
				require.NoError(t, err)
				for _, query := range []string{"DELETE FROM call_payload WHERE id LIKE ?", "DELETE FROM run WHERE id LIKE ?", "DELETE FROM conversation WHERE id LIKE ?"} {
					_, err := db.Exec(query, tag+"-%")
					require.NoError(t, err)
				}
			})
			old := time.Now().UTC().Add(-60 * 24 * time.Hour).Truncate(time.Second)
			tx, err := db.Begin()
			require.NoError(t, err)
			defer tx.Rollback()
			for _, id := range []string{root, foreign} {
				_, err = tx.Exec(`INSERT INTO conversation(id,status,created_at,updated_at,last_activity) VALUES(?,'completed',?,?,?)`, id, old, old, old)
				require.NoError(t, err)
				// Equal public protocol identities must not broaden native scope.
				_, err = tx.Exec(`INSERT INTO run(id,conversation_id,run_kind,protocol_run_id,protocol_status,protocol_revision,protocol_last_sequence,status,created_at,updated_at) VALUES(?,?,'agui','same-public-run','completed',1,400,'completed',?,?)`, id+"-run", id, old, old)
				require.NoError(t, err)
				count := n
				if id == foreign {
					count = 1
				}
				for i := 0; i < count; i++ {
					_, err = tx.Exec(`INSERT INTO call_payload(id,kind,mime_type,size_bytes,storage,inline_body,compression,run_id,sequence,created_at) VALUES(?,'agui.event','application/json',2,'inline','{}','none',?,?,?)`, fmt.Sprintf("%s-event-%03d", id, i), id+"-run", i+1, old)
					require.NoError(t, err)
				}
			}
			require.NoError(t, tx.Commit())
			_, err = db.Exec(fmt.Sprintf(`CREATE TRIGGER %s BEFORE DELETE ON conversation FOR EACH ROW BEGIN
 IF OLD.id='%s' THEN
  IF (SELECT COUNT(*) FROM call_payload WHERE run_id='%s-run' AND kind='agui.event')=0 THEN
   SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='forced rollback after event bulk';
  ELSE
   SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='events were not deleted before metadata';
  END IF;
 END IF;
END`, trigger, root, root))
			require.NoError(t, err)
			request := ConversationMaintenanceRequest{RootID: root, Kind: ConversationMaintenanceInteractive, InactiveBefore: old.Add(time.Hour), Mode: ConversationMaintenanceDelete, Lease: acquireTestMaintenanceLease(t, svc, tag+"-lease", tag)}
			out, err := svc.MaintainConversationTree(context.Background(), request)
			require.ErrorContains(t, err, "forced rollback after event bulk")
			require.Nil(t, out)
			assertStage1RowCount(t, db, "conversation", "id", root, 1)
			assertStage1RowCount(t, db, "run", "id", root+"-run", 1)
			assertStage1RowCount(t, db, "call_payload", "run_id", root+"-run", n)
			_, err = db.Exec("DROP TRIGGER " + trigger)
			require.NoError(t, err)
			out, err = svc.MaintainConversationTree(context.Background(), request)
			require.NoError(t, err)
			require.True(t, out.Deleted)
			require.False(t, out.Partial)
			require.Equal(t, 1, out.CommittedBatches, "no new transaction boundary for a small plan")
			require.EqualValues(t, n+2, out.AffectedRows, "events, one native protocol run and one conversation")
			assertStage1RowCount(t, db, "call_payload", "run_id", root+"-run", 0)
			assertStage1RowCount(t, db, "conversation", "id", foreign, 1)
			assertStage1RowCount(t, db, "run", "id", foreign+"-run", 1)
			assertStage1RowCount(t, db, "call_payload", "run_id", foreign+"-run", 1)
		})
	}
}
