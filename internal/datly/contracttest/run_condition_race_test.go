package tests

import (
	"context"
	"database/sql"
	"github.com/viant/bindly/locator"
	requestprovider "github.com/viant/bindly/provider/request"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRunConditionCompetingConnection(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type useCase struct {
		desc, input, statement string
		expect                 struct {
			status, owner string
			attempt       int
		}
	}
	for _, tc := range []useCase{
		{"owner changes after Current", `{"data":[{"id":"owned","status":"succeeded","condition":{"leaseOwner":"owner-a"}}]}`, `UPDATE run SET lease_owner='winner' WHERE id='owned'`, struct {
			status, owner string
			attempt       int
		}{"running", "winner", 1}},
		{"attempt changes after Current", `{"data":[{"id":"owned","attempt":2,"leaseOwner":"owner-b","condition":{"status":"running","attempt":1,"leaseOwner":"owner-a"}}]}`, `UPDATE run SET attempt=3 WHERE id='owned'`, struct {
			status, owner string
			attempt       int
		}{"running", "owner-a", 3}},
		{"status changes after Current", `{"data":[{"id":"owned","attempt":2,"leaseOwner":"owner-b","condition":{"status":"running","attempt":1,"leaseOwner":"owner-a"}}]}`, `UPDATE run SET status='failed' WHERE id='owned'`, struct {
			status, owner string
			attempt       int
		}{"failed", "owner-a", 1}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			db, path := runParityFixture(t, project)
			_, err := db.Exec(`PRAGMA journal_mode=WAL;UPDATE run SET lease_owner='owner-a' WHERE id='owned'`)
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
					_, competitorErr = competitor.Exec(tc.statement)
					if competitorErr == nil {
						winningSnapshot = sqliteRaceRowSnapshot(t, competitor, "run", "owned")
					}
					return competitorErr
				}}
			})
			request := httptest.NewRequest("PATCH", "/v1/api/agently/run", strings.NewReader(tc.input))
			request.Header.Set("Content-Type", "application/json")
			scope, err := requestprovider.New(request)
			must(t, err)
			defer scope.Close()
			_, err = rt.ExecuteRoute(context.Background(), "PATCH", "/v1/api/agently/run", scope)
			requireCommittedSQLiteSnapshotConflict(t, err, competitorErr)
			requireSQLiteWinnerUnchanged(t, db, "run", "owned", winningSnapshot)
			// The failed owned transaction has completed rollback before retry.
			rt, _ = runParityRuntime(t, db, "", true)
			freshRequest := httptest.NewRequest("PATCH", "/v1/api/agently/run", strings.NewReader(tc.input))
			freshRequest.Header.Set("Content-Type", "application/json")
			freshScope, freshErr := requestprovider.New(freshRequest)
			must(t, freshErr)
			defer freshScope.Close()
			_, err = rt.ExecuteRoute(context.Background(), "PATCH", "/v1/api/agently/run", freshScope)
			must(t, err)
			requireSQLiteWinnerUnchanged(t, db, "run", "owned", winningSnapshot)
			if !injected {
				t.Fatal("competing write was not injected")
			}
			var status, owner string
			var attempt int
			must(t, db.QueryRow("SELECT status,lease_owner,attempt FROM run WHERE id='owned'").Scan(&status, &owner, &attempt))
			if status != tc.expect.status || owner != tc.expect.owner || attempt != tc.expect.attempt {
				t.Fatalf("state=(%s,%s,%d) expected=%+v", status, owner, attempt, tc.expect)
			}
		})
	}
}

func TestRunConditionMixedBatchGuardLossRollsBack(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	db, _ := runParityFixture(t, project)
	_, err := db.Exec(`UPDATE run SET lease_owner=CASE WHEN id='owned' THEN 'owner-a' ELSE 'owner-other' END WHERE id IN ('owned','other')`)
	must(t, err)
	rt, _ := runParityRuntime(t, db, "", true)
	request := httptest.NewRequest("PATCH", "/v1/api/agently/run", strings.NewReader(`{"data":[{"id":"owned","status":"succeeded","condition":{"leaseOwner":"owner-a"}},{"id":"other","status":"succeeded","condition":{"leaseOwner":"stale"}}]}`))
	request.Header.Set("Content-Type", "application/json")
	scope, err := requestprovider.New(request)
	must(t, err)
	defer scope.Close()
	_, err = rt.ExecuteRoute(context.Background(), "PATCH", "/v1/api/agently/run", scope)
	if err == nil {
		t.Fatal("mixed batch guard loss was silently acknowledged")
	}
	var first, second string
	must(t, db.QueryRow("SELECT status FROM run WHERE id='owned'").Scan(&first))
	must(t, db.QueryRow("SELECT status FROM run WHERE id='other'").Scan(&second))
	if first != "running" || second != "failed" {
		t.Fatalf("partially written batch: %s,%s", first, second)
	}
}
