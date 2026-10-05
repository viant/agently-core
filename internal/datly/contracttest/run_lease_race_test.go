package tests

import (
	"context"
	"database/sql"
	write "github.com/viant/agently-core/internal/datly/run/write"
	"github.com/viant/bindly/locator"
	requestprovider "github.com/viant/bindly/provider/request"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRunLeaseCompetingConnection(t *testing.T) {
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
		{"claim loses to live competing owner", input{mode: "claim", statement: `UPDATE run SET lease_owner='competitor',lease_until='2026-01-04 00:00:00' WHERE id='lease'`}, expect{owner: "competitor", count: 1}},
		{"release cannot clear a changed owner", input{mode: "release", priorOwner: &worker, statement: `UPDATE run SET lease_owner='competitor',lease_until='2026-01-04 00:00:00' WHERE id='lease'`}, expect{owner: "competitor", count: 1}},
		{"claim cannot lease a concurrently completed run", input{mode: "claim", statement: `UPDATE run SET completed_at='2026-01-01 00:00:00' WHERE id='lease'`}, expect{count: 1}},
		{"claim cannot recreate a concurrently deleted run", input{mode: "claim", statement: `DELETE FROM run WHERE id='lease'`}, expect{}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			db, path := goalFixture(t, project)
			_, err := db.Exec(`PRAGMA journal_mode=WAL;INSERT INTO run(id,status,lease_owner,created_at) VALUES('lease','running',?,'2026-01-01 00:00:00')`, tc.input.priorOwner)
			must(t, err)
			competitor, err := sql.Open("sqlite3", path+"?_busy_timeout=5000")
			must(t, err)
			defer competitor.Close()
			injected := false
			var competitorErr error
			var winningSnapshot map[string]any
			rt, _ := runParityRuntimeWithViewHook(t, db, "", true, func(provider locator.Provider) locator.Provider {
				return &afterScheduleView{Provider: provider, after: func() error {
					injected = true
					_, competitorErr = competitor.Exec(tc.input.statement)
					if competitorErr == nil {
						winningSnapshot = sqliteRaceRowSnapshot(t, competitor, "run", "lease")
					}
					return competitorErr
				}}
			})
			request := httptest.NewRequest("PATCH", "/v1/api/agently/run?leaseMode="+tc.input.mode+"&leaseOwner=worker&leaseNow=2026-01-02T00:00:00Z", strings.NewReader(`{"data":[{"id":"lease","leaseUntil":"2026-01-03T00:00:00Z"}]}`))
			request.Header.Set("Content-Type", "application/json")
			scope, err := requestprovider.New(request)
			must(t, err)
			defer scope.Close()
			out, err := rt.ExecuteRoute(context.Background(), "PATCH", "/v1/api/agently/run", scope)
			requireCommittedSQLiteSnapshotConflict(t, err, competitorErr)
			requireSQLiteWinnerUnchanged(t, db, "run", "lease", winningSnapshot)
			// The failed owned transaction has completed rollback before retry.
			rt, _ = runParityRuntime(t, db, "", true)
			freshRequest := httptest.NewRequest("PATCH", "/v1/api/agently/run?leaseMode="+tc.input.mode+"&leaseOwner=worker&leaseNow=2026-01-02T00:00:00Z", strings.NewReader(`{"data":[{"id":"lease","leaseUntil":"2026-01-03T00:00:00Z"}]}`))
			freshRequest.Header.Set("Content-Type", "application/json")
			freshScope, freshErr := requestprovider.New(freshRequest)
			must(t, freshErr)
			defer freshScope.Close()
			out, err = rt.ExecuteRoute(context.Background(), "PATCH", "/v1/api/agently/run", freshScope)
			must(t, err)
			requireSQLiteWinnerUnchanged(t, db, "run", "lease", winningSnapshot)
			if !injected {
				t.Fatal("competing write was not injected after Current lookup")
			}
			if out.(*write.Output).LeaseResult != tc.expect.result {
				t.Fatalf("lease result=%v expected=%v", out.(*write.Output).LeaseResult, tc.expect.result)
			}
			var count int
			must(t, db.QueryRow("SELECT COUNT(*) FROM run WHERE id='lease'").Scan(&count))
			if count != tc.expect.count {
				t.Fatalf("count=%d expected=%d", count, tc.expect.count)
			}
			if count != 0 {
				var owner sql.NullString
				must(t, db.QueryRow("SELECT lease_owner FROM run WHERE id='lease'").Scan(&owner))
				if owner.String != tc.expect.owner {
					t.Fatalf("owner=%q expected=%q", owner.String, tc.expect.owner)
				}
			}
		})
	}
}
