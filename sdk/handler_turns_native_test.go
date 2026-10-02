package sdk

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/store/data"
	"github.com/viant/agently-core/app/store/native"
	authctx "github.com/viant/agently-core/internal/auth"
	convsvc "github.com/viant/agently-core/internal/service/conversation"
	"github.com/viant/agently-core/internal/testutil/dbtest"
	"github.com/viant/datly/bootstrap/connector"
)

func TestHTTPQueuedTurnCancellationNative(t *testing.T) {
	for _, driver := range []string{"sqlite3", "mysql"} {
		t.Run(driver, func(t *testing.T) {
			var db *sql.DB
			var dsn string
			if driver == "mysql" {
				dsn = os.Getenv("AGENTLY_TEST_MYSQL_DSN")
				if dsn == "" {
					t.Skip("AGENTLY_TEST_MYSQL_DSN is not set")
				}
				var err error
				db, err = sql.Open(driver, dsn)
				require.NoError(t, err)
				t.Cleanup(func() { require.NoError(t, db.Close()) })
			} else {
				var cleanup func()
				db, dsn, cleanup = dbtest.CreateTempSQLiteDB(t, "queued-turn-http")
				t.Cleanup(cleanup)
				dbtest.LoadSQLiteSchema(t, db)
			}
			ctx := context.Background()
			_, file, _, _ := runtime.Caller(0)
			host, err := native.New(ctx, native.Options{SourceRoot: filepath.Join(filepath.Dir(file), ".."), Connectors: []connector.Config{{Name: "agently", Driver: driver, DSN: dsn}}})
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, host.Shutdown(context.Background())) })
			conv, err := convsvc.New(ctx, host)
			require.NoError(t, err)
			backend := &backendClient{data: data.NewService(host), conv: conv}
			prefix := fmt.Sprintf("queued-http-%d", time.Now().UnixNano())
			cid, tid, mid := prefix+"-c", prefix+"-t", prefix+"-m"
			_, err = db.Exec("INSERT INTO conversation(id,created_by_user_id,created_at) VALUES(?, ?, CURRENT_TIMESTAMP)", cid, "queued-http-owner")
			require.NoError(t, err)
			t.Cleanup(func() {
				_, err := db.Exec("DELETE FROM conversation WHERE id=?", cid)
				require.NoError(t, err)
			})
			_, err = db.Exec("INSERT INTO turn(id,conversation_id,status,started_by_message_id,created_at) VALUES(?,?,'queued',?,CURRENT_TIMESTAMP)", tid, cid, mid)
			require.NoError(t, err)
			_, err = db.Exec("INSERT INTO message(id,conversation_id,turn_id,role,type,created_at) VALUES(?,?,?,'user','text',CURRENT_TIMESTAMP)", mid, cid, tid)
			require.NoError(t, err)
			_, err = db.Exec("INSERT INTO turn_queue(id,conversation_id,turn_id,message_id,queue_seq,status,created_at) VALUES(?,?,?,?,1,'queued',CURRENT_TIMESTAMP)", tid, cid, tid, mid)
			require.NoError(t, err)

			mux := http.NewServeMux()
			mux.HandleFunc("DELETE /v1/conversations/{id}/turns/{turnId}", handleDeleteQueuedTurn(backend))
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mux.ServeHTTP(w, r.WithContext(authctx.WithUserInfo(r.Context(), &authctx.UserInfo{Subject: "queued-http-owner"})))
			}))
			defer server.Close()
			cancel := func(want int) {
				req, err := http.NewRequest(http.MethodDelete, server.URL+"/v1/conversations/"+cid+"/turns/"+tid, nil)
				require.NoError(t, err)
				res, err := server.Client().Do(req)
				require.NoError(t, err)
				body, err := io.ReadAll(res.Body)
				require.NoError(t, err)
				require.NoError(t, res.Body.Close())
				require.Equal(t, want, res.StatusCode, string(body))
			}
			cancel(http.StatusNoContent)
			for _, expected := range []struct{ table, id, status string }{{"turn", tid, "canceled"}, {"turn_queue", tid, "canceled"}, {"message", mid, "cancel"}} {
				var status string
				require.NoError(t, db.QueryRow("SELECT status FROM "+expected.table+" WHERE id=?", expected.id).Scan(&status))
				require.Equal(t, expected.status, status, expected.table)
			}
			_, err = db.Exec("UPDATE turn SET status='succeeded' WHERE id=?", tid)
			require.NoError(t, err)
			cancel(http.StatusConflict)
			var status string
			require.NoError(t, db.QueryRow("SELECT status FROM turn WHERE id=?", tid).Scan(&status))
			require.Equal(t, "succeeded", status)
		})
	}
}
