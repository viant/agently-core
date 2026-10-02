package tests

import (
	"bytes"
	"context"
	"encoding/json"
	schedule "github.com/viant/agently-core/internal/datly/schedule/read"
	write "github.com/viant/agently-core/internal/datly/schedule/write"
	requestprovider "github.com/viant/bindly/provider/request"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/spec"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestScheduleLeaseLegacyV1Parity(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	legacy := legacyProbeBinary(t, project)
	type input struct {
		Action                       string
		Enabled                      int
		PreviousOwner, PreviousUntil *string
		Request                      map[string]string
	}
	type expect struct{ Result bool }
	type useCase struct {
		Desc   string
		Input  input
		Expect expect
	}
	raw, err := os.ReadFile(filepath.Join(project, "migration/schedule-lease-legacy-baseline.json"))
	must(t, err)
	var cases []useCase
	must(t, json.Unmarshal(raw, &cases))
	for _, tc := range cases {
		t.Run(tc.Desc, func(t *testing.T) {
			oldDB, oldPath := goalFixture(t, project)
			db, _ := goalFixture(t, project)
			seed := `INSERT INTO schedule(id,name,agent_ref,enabled,lease_owner,lease_until,created_at) VALUES('lease','lease','fixture',?,?,?,'2026-01-01 00:00:00')`
			_, err = oldDB.Exec(seed, tc.Input.Enabled, tc.Input.PreviousOwner, tc.Input.PreviousUntil)
			must(t, err)
			_, err = db.Exec(seed, tc.Input.Enabled, tc.Input.PreviousOwner, tc.Input.PreviousUntil)
			must(t, err)
			body, err := json.Marshal(tc.Input.Request)
			must(t, err)
			payload, err := json.Marshal(map[string]any{"Component": "scheduleLease", "DBPath": oldPath, "Body": string(body), "Filters": map[string]any{"action": tc.Input.Action}})
			must(t, err)
			process := exec.Command(legacy)
			process.Stdin = bytes.NewReader(payload)
			var stderr bytes.Buffer
			process.Stderr = &stderr
			raw, err := process.Output()
			if err != nil {
				t.Fatalf("legacy: %v\n%s", err, stderr.String())
			}
			var before probeResult
			must(t, json.Unmarshal(raw, &before))
			if before.Failed {
				t.Fatalf("legacy failed: %s", before.Error)
			}
			var oldResult bool
			must(t, json.Unmarshal(before.Output, &oldResult))
			rt, key := scheduleWriterRuntime(t, db, "")
			row := map[string]any{"id": tc.Input.Request["scheduleId"]}
			if tc.Input.Action == "claim" {
				row["leaseUntil"] = tc.Input.Request["leaseUntil"]
			}
			body, err = json.Marshal(map[string]any{"data": []any{row}})
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
			if oldResult != tc.Expect.Result || newResult != tc.Expect.Result {
				t.Fatalf("lease result old=%v new=%v expected=%v", oldResult, newResult, tc.Expect.Result)
			}
			value, err = rt.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "GET", Path: "/v1/api/agently/scheduler/schedule/{id}"}}, Input: &schedule.ScheduleInput{}})
			must(t, err)
			raw, err = json.Marshal(value.(*schedule.ScheduleOutput).Data)
			must(t, err)
			var rows []json.RawMessage
			must(t, json.Unmarshal(raw, &rows))
			oldRows, newRows := normalizeRows(t, before.Rows), normalizeRows(t, rows)
			if !reflect.DeepEqual(oldRows, newRows) {
				t.Fatalf("lease state\nold=%s\nnew=%s", pretty(oldRows), pretty(newRows))
			}
		})
	}
}
