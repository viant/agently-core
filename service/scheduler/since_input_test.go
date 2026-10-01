package scheduler

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/require"
	storedata "github.com/viant/agently-core/app/store/data"
	"github.com/viant/agently-core/app/store/native"
	legacy "github.com/viant/agently-core/pkg/agently/scheduler/run"
	svcauth "github.com/viant/agently-core/service/auth"
	"github.com/viant/datly/bootstrap/connector"
)

func TestSchedulerSinceTurnSQLite(t *testing.T) {
	store, db := newTestStore(t)
	runSinceTurnCases(t, store, db, "since-sqlite-")
}
func TestSchedulerSinceTurnMySQL(t *testing.T) {
	dsn := os.Getenv("AGENTLY_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("AGENTLY_TEST_MYSQL_DSN is not set")
	}
	db, err := sql.Open("mysql", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	prefix := fmt.Sprintf("agently-migration-mysql-since-%d-", time.Now().UnixNano())
	t.Cleanup(func() {
		for _, item := range []struct{ table, key string }{{"run", "id"}, {"turn", "id"}, {"schedule", "id"}, {"conversation", "id"}} {
			_, err := db.Exec("DELETE FROM "+item.table+" WHERE "+item.key+" LIKE ?", prefix+"%")
			require.NoError(t, err)
		}
	})
	_, file, _, _ := runtime.Caller(0)
	server, err := native.New(context.Background(), native.Options{SourceRoot: filepath.Join(filepath.Dir(file), "..", ".."), Connectors: []connector.Config{{Name: "agently", Driver: "mysql", DSN: dsn}}})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Shutdown(context.Background())) })
	store, err := NewDatlyStore(context.Background(), server, storedata.NewService(server))
	require.NoError(t, err)
	runSinceTurnCases(t, store, db, prefix)
}
func runSinceTurnCases(t *testing.T, store Store, db *sql.DB, prefix string) {
	t.Helper()
	owner, other := prefix+"owner", prefix+"other"
	conversation, anchor, owned, foreign := prefix+"conversation", prefix+"anchor", prefix+"owned", prefix+"foreign"
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	_, err := db.Exec("INSERT INTO conversation(id,created_at,status) VALUES(?,?,'succeeded')", conversation, base)
	require.NoError(t, err)
	_, err = db.Exec("INSERT INTO turn(id,conversation_id,created_at,status) VALUES(?,?,?,'succeeded')", anchor, conversation, base.Add(24*time.Hour))
	require.NoError(t, err)
	for _, row := range []struct{ id, subject string }{{owned, owner}, {foreign, other}} {
		_, err = db.Exec("INSERT INTO schedule(id,name,visibility,created_by_user_id,agent_ref,schedule_type,timezone,created_at,updated_at) VALUES(?,?,'private',?,'fixture','adhoc','UTC',?,?)", row.id, row.id, row.subject, base, base)
		require.NoError(t, err)
	}
	for i, row := range []struct{ id, schedule, subject string }{{prefix + "before", owned, owner}, {prefix + "equal", owned, owner}, {prefix + "after", owned, owner}, {prefix + "foreign-run", foreign, other}} {
		created := base.Add(time.Duration(i) * 24 * time.Hour)
		_, err = db.Exec("INSERT INTO run(id,schedule_id,conversation_id,conversation_kind,status,effective_user_id,created_at,started_at,updated_at) VALUES(?,?,?,'scheduled','succeeded',?,?,?,?)", row.id, row.schedule, conversation, row.subject, created, created, created)
		require.NoError(t, err)
	}
	handler := NewHandler(New(store, nil))
	mux := http.NewServeMux()
	handler.RegisterWithoutRunNow(mux)
	// The domain fixture injects a fixed authenticated subject before the real
	// HTTP handler; cryptographic JWT/cookie checks are covered in e2e/auth.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mux.ServeHTTP(w, r.WithContext(svcauth.InjectUser(r.Context(), owner)))
	}))
	t.Cleanup(srv.Close)
	for _, test := range []struct {
		name, value string
		present     bool
		total, rows int
	}{{"absent", "", false, 3, 3}, {"anchor", anchor, true, 2, 2}, {"empty", "", true, 3, 3}, {"wrong turn", prefix + "missing", true, 0, 0}, {"literal ISO is not a timestamp", "2026-01-02T00:00:00Z", true, 0, 0}, {"whitespace is literal", " " + anchor + " ", true, 0, 0}} {
		t.Run(test.name, func(t *testing.T) {
			q := url.Values{"scheduleId": {owned}, "size": {"10"}}
			if test.present {
				q.Set("since", test.value)
			}
			response, err := srv.Client().Get(srv.URL + "/v1/api/agently/scheduler/run?" + q.Encode())
			require.NoError(t, err)
			defer response.Body.Close()
			require.Equal(t, http.StatusOK, response.StatusCode)
			var body struct {
				Data []*legacy.RunView `json:"data"`
				Info struct {
					TotalCount int `json:"totalCount"`
					PageCount  int `json:"pageCount"`
				} `json:"info"`
			}
			require.NoError(t, json.NewDecoder(response.Body).Decode(&body))
			require.Len(t, body.Data, test.rows)
			require.Equal(t, test.total, body.Info.TotalCount)
		})
	}
	ctx := svcauth.InjectUser(context.Background(), owner)
	input := &legacy.RunListInput{ScheduleId: owned, Since: anchor, Has: &legacy.RunListInputHas{ScheduleId: true}}
	page, err := store.ListRuns(ctx, input, 1, 1)
	require.NoError(t, err)
	require.Equal(t, 3, page.TotalCount, "unmarked Since must be absent")
	input.Has.Since = true
	page, err = store.ListRuns(ctx, input, 1, 1)
	require.NoError(t, err)
	require.Equal(t, 2, page.TotalCount)
	require.Equal(t, 2, page.PageCount)
	require.Len(t, page.Rows, 1)
	require.Equal(t, prefix+"after", page.Rows[0].Id)
	page, err = store.ListRuns(ctx, input, 2, 1)
	require.NoError(t, err)
	require.Equal(t, 2, page.TotalCount)
	require.Len(t, page.Rows, 1)
	require.Equal(t, prefix+"equal", page.Rows[0].Id)
	page, err = store.ListRuns(svcauth.InjectUser(context.Background(), other), input, 1, 10)
	require.NoError(t, err)
	require.Zero(t, page.TotalCount)
	require.Empty(t, page.Rows, "owner scope must remain outside optional since filter")
}
