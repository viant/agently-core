package tests

import (
	"context"
	"encoding/json"
	write "github.com/viant/agently-core/internal/datly/schedule/write"
	requestprovider "github.com/viant/bindly/provider/request"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestScheduleLeaseLegacyV1Parity(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type input struct {
		Action                       string
		Enabled                      int
		PreviousOwner, PreviousUntil *string
		Request                      map[string]string
	}
	type expect struct {
		Result       bool
		Owner, Until *string
	}
	type useCase struct {
		Desc   string
		Input  input
		Expect expect
	}
	raw, err := os.ReadFile(filepath.Join(project, "internal/datly/contracttest/testdata/schedule-lease-legacy-baseline.json"))
	must(t, err)
	var cases []useCase
	must(t, json.Unmarshal(raw, &cases))
	for _, tc := range cases {
		t.Run(tc.Desc, func(t *testing.T) {
			db, _ := goalFixture(t, project)
			seed := `INSERT INTO schedule(id,name,agent_ref,enabled,lease_owner,lease_until,created_at) VALUES('lease','lease','fixture',?,?,?,'2026-01-01 00:00:00')`
			_, err = db.Exec(seed, tc.Input.Enabled, tc.Input.PreviousOwner, tc.Input.PreviousUntil)
			must(t, err)
			rt, _ := scheduleWriterRuntime(t, db, "")
			row := map[string]any{"id": tc.Input.Request["scheduleId"]}
			if tc.Input.Action == "claim" {
				row["leaseUntil"] = tc.Input.Request["leaseUntil"]
			}
			body, err := json.Marshal(map[string]any{"data": []any{row}})
			must(t, err)
			query := url.Values{"leaseMode": {tc.Input.Action}, "leaseOwner": {tc.Input.Request["leaseOwner"]}, "leaseNow": {tc.Input.Request["now"]}}
			req := httptest.NewRequest("PATCH", "/v1/api/agently/scheduler/?"+query.Encode(), strings.NewReader(string(body)))
			req.Header.Set("Content-Type", "application/json")
			scope, err := requestprovider.New(req)
			must(t, err)
			defer scope.Close()
			value, err := rt.ExecuteRoute(context.Background(), "PATCH", "/v1/api/agently/scheduler/", scope)
			must(t, err)
			newResult := value.(*write.Output).LeaseResult
			if newResult != tc.Expect.Result {
				t.Fatalf("lease result=%v expected=%v", newResult, tc.Expect.Result)
			}
			var owner, until string
			must(t, db.QueryRow("SELECT COALESCE(lease_owner, '<null>'), COALESCE(lease_until, '<null>') FROM schedule WHERE id='lease'").Scan(&owner, &until))
			wantOwner, wantUntil := "<null>", "<null>"
			if tc.Expect.Owner != nil {
				wantOwner = *tc.Expect.Owner
			}
			if tc.Expect.Until != nil {
				wantUntil = *tc.Expect.Until
			}
			wantUntil = strings.Replace(wantUntil, " +0000 UTC", "+00:00", 1)
			if owner != wantOwner || until != wantUntil {
				t.Fatalf("lease state=(%q,%q) expected=(%q,%q)", owner, until, wantOwner, wantUntil)
			}
		})
	}
}
