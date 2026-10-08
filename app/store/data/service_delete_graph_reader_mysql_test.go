package data

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/require"
	tree "github.com/viant/agently-core/internal/store/conversationtree"
	xexec "github.com/viant/xdatly/exec"
)

// Uses only uniquely tagged rows in the existing dedicated local benchmark DB;
// never creates schema and refuses any ordinary/local-production database.
func TestDeleteGraphReaderMySQL(t *testing.T) {
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
	for _, mode := range []string{"legacy", "compact"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv(tree.GraphReaderEnvironment, mode)
			tag := fmt.Sprintf("graph-reader-%s-%d", mode, time.Now().UnixNano())
			root, child := tag+"-root", tag+"-child"
			t.Cleanup(func() {
				_, err := db.Exec("DELETE FROM turn WHERE id IN (?,?)", root+"-turn", child+"-turn")
				require.NoError(t, err)
				_, err = db.Exec("DELETE FROM conversation WHERE id=?", child)
				require.NoError(t, err)
				_, err = db.Exec("DELETE FROM conversation WHERE id=?", root)
				require.NoError(t, err)
			})
			_, err := db.Exec(`INSERT INTO conversation(id,created_by_user_id,status,created_at)
                VALUES(?,'u1','succeeded','2026-01-01 00:00:00')`, root)
			require.NoError(t, err)
			_, err = db.Exec(`INSERT INTO conversation(id,created_by_user_id,status,conversation_parent_id,created_at)
                VALUES(?,'u1',NULL,?,'2026-01-01 00:00:01')`, child, root)
			require.NoError(t, err)
			_, err = db.Exec(`INSERT INTO turn(id,conversation_id,status) VALUES(?,?,'succeeded'),(?,?,'succeeded')`, root+"-turn", root, child+"-turn", child)
			require.NoError(t, err)
			d := tree.NewSystemDiscoverer(svc.(*datlyService).native, nil)
			graph, err := d.Discover(context.Background(), root)
			require.NoError(t, err)
			require.Len(t, graph.Nodes, 2)
			require.Empty(t, graph.Nodes[child].Status)
			require.Equal(t, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), graph.Nodes[root].CreatedAt)
			require.Equal(t, 1, graph.Nodes[child].Depth)
			ec := xexec.New()
			require.NoError(t, svc.DeleteConversationTree(xexec.WithContext(deleteTestContext(), ec), root))
			if mode == "compact" {
				var sawRead, sawLock bool
				for _, m := range ec.SnapshotForLogging().Metrics {
					if m == nil {
						continue
					}
					for _, e := range m.Executions {
						if e == nil {
							continue
						}
						q := strings.ToLower(e.SQL)
						if strings.Contains(q, "from conversation c") && strings.Contains(q, "created_at_raw") {
							sawRead = true
							sawLock = sawLock || strings.Contains(q, "for update")
							require.NotContains(t, q, "inline_body")
							require.NotContains(t, q, " join ")
						}
					}
				}
				require.True(t, sawRead, "capture the executed compact SELECT")
				require.True(t, sawLock, "compact reader must acquire real MySQL row locks")
			}
			var remaining int
			require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM conversation WHERE id IN (?,?)", root, child).Scan(&remaining))
			require.Zero(t, remaining)
		})
	}
}
