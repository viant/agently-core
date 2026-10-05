package tests

import (
	"context"
	"database/sql"
	"errors"
	sqlite3 "github.com/mattn/go-sqlite3"
	write "github.com/viant/agently-core/internal/datly/schedule/write"
	"github.com/viant/bindly/locator"
	requestprovider "github.com/viant/bindly/provider/request"
	"github.com/viant/structology"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

type afterScheduleView struct {
	locator.Provider
	after func() error
}

func (p *afterScheduleView) DefaultCacheable() bool { return false }
func (p *afterScheduleView) Locate(state *structology.State) locator.Locator {
	return &afterScheduleLocator{Locator: p.Provider.Locate(state), after: p.after}
}

type afterScheduleLocator struct {
	locator.Locator
	after func() error
	done  bool
}

func (l *afterScheduleLocator) ValueInScope(ctx context.Context, scope locator.Scope, target reflect.Type, name string) (any, bool, error) {
	value, found, err := l.Locator.(locator.ScopedLocator).ValueInScope(ctx, scope, target, name)
	if err == nil && found && name == "CurrentWriter" && !l.done {
		l.done = true
		err = l.after()
	}
	return value, found, err
}

func TestScheduleLeaseCompetingConnection(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type input struct {
		mode       string
		priorOwner *string
		statement  string
	}
	type expect struct {
		result bool
		owner  string
		count  int
	}
	type useCase struct {
		desc   string
		input  input
		expect expect
	}
	worker := "worker"
	for _, tc := range []useCase{
		{"claim loses to live competing owner", input{mode: "claim", statement: `UPDATE schedule SET lease_owner='competitor',lease_until='2026-01-04 00:00:00' WHERE id='lease'`}, expect{owner: "competitor", count: 1}},
		{"release cannot clear a changed owner", input{mode: "release", priorOwner: &worker, statement: `UPDATE schedule SET lease_owner='competitor',lease_until='2026-01-04 00:00:00' WHERE id='lease'`}, expect{owner: "competitor", count: 1}},
		{"claim cannot reenable a concurrently disabled schedule", input{mode: "claim", statement: `UPDATE schedule SET enabled=0 WHERE id='lease'`}, expect{count: 1}},
		{"claim cannot recreate a concurrently deleted schedule", input{mode: "claim", statement: `DELETE FROM schedule WHERE id='lease'`}, expect{}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			db, path := goalFixture(t, project)
			_, err := db.Exec(`PRAGMA journal_mode=WAL;INSERT INTO schedule(id,name,agent_ref,lease_owner,created_at) VALUES('lease','lease','fixture',?,'2026-01-01 00:00:00')`, tc.input.priorOwner)
			must(t, err)
			competitor, err := sql.Open("sqlite3", path+"?_busy_timeout=5000")
			must(t, err)
			defer competitor.Close()
			injected := false
			var competitorErr error
			var winningSnapshot map[string]any
			rt, _ := scheduleWriterRuntimeWithViewHook(t, db, "", func(provider locator.Provider) locator.Provider {
				return &afterScheduleView{Provider: provider, after: func() error {
					injected = true
					_, competitorErr = competitor.Exec(tc.input.statement)
					if competitorErr == nil {
						winningSnapshot = sqliteRaceRowSnapshot(t, competitor, "schedule", "lease")
					}
					return competitorErr
				}}
			})
			request := httptest.NewRequest("PATCH", "/v1/api/agently/scheduler/?leaseMode="+tc.input.mode+"&leaseOwner=worker&leaseNow=2026-01-02T00:00:00Z", strings.NewReader(`{"data":[{"id":"lease","leaseUntil":"2026-01-03T00:00:00Z"}]}`))
			request.Header.Set("Content-Type", "application/json")
			scope, err := requestprovider.New(request)
			must(t, err)
			defer scope.Close()
			out, err := rt.ExecuteRoute(context.Background(), "PATCH", "/v1/api/agently/scheduler/", scope)
			requireCommittedSQLiteSnapshotConflict(t, err, competitorErr)
			requireSQLiteWinnerUnchanged(t, db, "schedule", "lease", winningSnapshot)
			// The failed owned transaction has completed rollback before retry.
			rt, _ = scheduleWriterRuntime(t, db, "")
			freshRequest := httptest.NewRequest("PATCH", "/v1/api/agently/scheduler/?leaseMode="+tc.input.mode+"&leaseOwner=worker&leaseNow=2026-01-02T00:00:00Z", strings.NewReader(`{"data":[{"id":"lease","leaseUntil":"2026-01-03T00:00:00Z"}]}`))
			freshRequest.Header.Set("Content-Type", "application/json")
			freshScope, freshErr := requestprovider.New(freshRequest)
			must(t, freshErr)
			defer freshScope.Close()
			out, err = rt.ExecuteRoute(context.Background(), "PATCH", "/v1/api/agently/scheduler/", freshScope)
			must(t, err)
			requireSQLiteWinnerUnchanged(t, db, "schedule", "lease", winningSnapshot)
			if !injected {
				t.Fatal("competing write was not injected after Current lookup")
			}
			if out.(*write.Output).LeaseResult != tc.expect.result {
				t.Fatalf("lease result=%v expected=%v", out.(*write.Output).LeaseResult, tc.expect.result)
			}
			var count int
			must(t, db.QueryRow("SELECT COUNT(*) FROM schedule WHERE id='lease'").Scan(&count))
			if count != tc.expect.count {
				t.Fatalf("count=%d expected=%d", count, tc.expect.count)
			}
			if count != 0 {
				var owner sql.NullString
				must(t, db.QueryRow("SELECT lease_owner FROM schedule WHERE id='lease'").Scan(&owner))
				if owner.String != tc.expect.owner {
					t.Fatalf("owner=%q expected=%q", owner.String, tc.expect.owner)
				}
			}
		})
	}
}

// Current is now read inside the writer transaction. In WAL mode a competing
// committed writer invalidates that snapshot: upgrading it must fail and roll
// back, rather than acknowledge a stale mutation. A fresh invocation below
// separately verifies the application's guarded no-op result.
func requireCommittedSQLiteSnapshotConflict(t *testing.T, err, competitorErr error) {
	t.Helper()
	if competitorErr != nil {
		t.Fatalf("competitor did not commit: %v", competitorErr)
	}
	var sqliteErr sqlite3.Error
	if err == nil || !errors.As(err, &sqliteErr) {
		t.Fatalf("expected typed SQLite snapshot conflict, got %T: %v", err, err)
	}
	if sqliteErr.Code != sqlite3.ErrBusy || sqliteErr.ExtendedCode != sqlite3.ErrBusySnapshot {
		t.Fatalf("unexpected SQLite conflict code=%d extended=%d: %v", sqliteErr.Code, sqliteErr.ExtendedCode, err)
	}
}

func sqliteRaceRowSnapshot(t *testing.T, db *sql.DB, table, id string) map[string]any {
	t.Helper()
	if table != "run" && table != "schedule" {
		t.Fatal("unexpected fixture table")
	}
	rows, err := db.Query("SELECT * FROM "+table+" WHERE id=?", id)
	must(t, err)
	defer rows.Close()
	if !rows.Next() {
		must(t, rows.Err())
		return nil
	}
	columns, err := rows.Columns()
	must(t, err)
	values := make([]any, len(columns))
	dest := make([]any, len(columns))
	for i := range values {
		dest[i] = &values[i]
	}
	must(t, rows.Scan(dest...))
	snapshot := map[string]any{}
	for i, column := range columns {
		value := values[i]
		if b, ok := value.([]byte); ok {
			value = string(b)
		}
		snapshot[column] = value
	}
	return snapshot
}
func requireSQLiteWinnerUnchanged(t *testing.T, db *sql.DB, table, id string, expected map[string]any) {
	t.Helper()
	actual := sqliteRaceRowSnapshot(t, db, table, id)
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("competing winner changed: actual=%#v expected=%#v", actual, expected)
	}
}
