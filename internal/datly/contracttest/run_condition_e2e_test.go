package tests

import (
	"bytes"
	"context"
	"encoding/json"
	read "github.com/viant/agently-core/internal/datly/run/read"
	write "github.com/viant/agently-core/internal/datly/run/write"
	requestprovider "github.com/viant/bindly/provider/request"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/spec"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestRunConditionLegacyV1Parity(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	legacy := legacyProbeBinary(t, project)
	type expect struct {
		legacyPanic   bool
		failed        bool
		status, owner string
		attempt       int
	}
	type useCase struct {
		desc, input string
		expect      expect
	}
	for _, tc := range []useCase{
		{"owned sparse update matches", `{"data":[{"id":"owned","status":"succeeded","condition":{"leaseOwner":"owner-a"}}]}`, expect{status: "succeeded", owner: "owner-a", attempt: 1}},
		{"stale owner is an acknowledged no-op", `{"data":[{"id":"owned","status":"succeeded","condition":{"leaseOwner":"other"}}]}`, expect{status: "running", owner: "owner-a", attempt: 1}},
		{"unchanged value still checks owner", `{"data":[{"id":"owned","status":"running","condition":{"leaseOwner":"other"}}]}`, expect{status: "running", owner: "owner-a", attempt: 1}},
		{"claim matches status attempt and owner", `{"data":[{"id":"owned","attempt":2,"leaseOwner":"owner-b","condition":{"status":"running","attempt":1,"leaseOwner":"owner-a"}}]}`, expect{status: "running", owner: "owner-b", attempt: 2}},
		{"stale claim attempt is a no-op", `{"data":[{"id":"owned","attempt":2,"leaseOwner":"owner-b","condition":{"status":"running","attempt":0,"leaseOwner":"owner-a"}}]}`, expect{status: "running", owner: "owner-a", attempt: 1}},
		{"stale claim status is a no-op", `{"data":[{"id":"owned","attempt":2,"leaseOwner":"owner-b","condition":{"status":"pending","attempt":1,"leaseOwner":"owner-a"}}]}`, expect{status: "running", owner: "owner-a", attempt: 1}},
		{"owner rotation is atomic", `{"data":[{"id":"owned","leaseOwner":"owner-b","condition":{"leaseOwner":"owner-a"}}]}`, expect{status: "running", owner: "owner-b", attempt: 1}},
		{"expected owner trims whitespace", `{"data":[{"id":"owned","status":"succeeded","condition":{"leaseOwner":" owner-a "}}]}`, expect{status: "succeeded", owner: "owner-a", attempt: 1}},
		{"missing conditional target cannot insert", `{"data":[{"id":"absent","status":"running","condition":{"leaseOwner":"owner-a"}}]}`, expect{failed: true, status: "running", owner: "owner-a", attempt: 1}},
		{"owner guard cannot change attempt", `{"data":[{"id":"owned","attempt":2,"condition":{"leaseOwner":"owner-a"}}]}`, expect{failed: true, status: "running", owner: "owner-a", attempt: 1}},
		{"claim cannot change status", `{"data":[{"id":"owned","status":"succeeded","condition":{"status":"running","attempt":1,"leaseOwner":"owner-a"}}]}`, expect{failed: true, status: "running", owner: "owner-a", attempt: 1}},
		{"partial claim expectation is rejected", `{"data":[{"id":"owned","leaseOwner":"owner-b","condition":{"status":"running","leaseOwner":"owner-a"}}]}`, expect{failed: true, status: "running", owner: "owner-a", attempt: 1}},
		{"empty owner expectation is rejected", `{"data":[{"id":"owned","status":"succeeded","condition":{"leaseOwner":""}}]}`, expect{failed: true, status: "running", owner: "owner-a", attempt: 1}},
		{"distinct per-row expectations match together", `{"data":[{"id":"owned","status":"succeeded","condition":{"leaseOwner":"owner-a"}},{"id":"other","status":"succeeded","condition":{"leaseOwner":"owner-other"}}]}`, expect{status: "succeeded", owner: "owner-a", attempt: 1}},
		{"guarded and ordinary rows share a batch", `{"data":[{"id":"owned","status":"succeeded","condition":{"leaseOwner":"owner-a"}},{"id":"other","errorMessage":"changed"}]}`, expect{legacyPanic: true, status: "succeeded", owner: "owner-a", attempt: 1}},
		{"unsupported conditional column is rejected", `{"data":[{"id":"owned","workerId":"new","condition":{"leaseOwner":"owner-a"}}]}`, expect{failed: true, status: "running", owner: "owner-a", attempt: 1}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			oldDB, oldPath := runParityFixture(t, project)
			db, _ := runParityFixture(t, project)
			_, err := oldDB.Exec("UPDATE run SET lease_owner=CASE WHEN id='owned' THEN 'owner-a' ELSE 'owner-other' END WHERE id IN ('owned','other')")
			must(t, err)
			_, err = db.Exec("UPDATE run SET lease_owner=CASE WHEN id='owned' THEN 'owner-a' ELSE 'owner-other' END WHERE id IN ('owned','other')")
			must(t, err)
			payload, err := json.Marshal(map[string]any{"Component": "run", "DBPath": oldPath, "Raw": true, "Body": tc.input})
			must(t, err)
			process := exec.Command(legacy)
			process.Stdin = bytes.NewReader(payload)
			var stderr bytes.Buffer
			process.Stderr = &stderr
			raw, err := process.Output()
			if tc.expect.legacyPanic {
				if err == nil || !strings.Contains(stderr.String(), "not assignable to type *write.runOwnedMutation") {
					t.Fatalf("legacy mixed-shape behavior changed: %v", err)
				}
				// Read the real legacy database after its mixed-shape process fails.
				payload, err = json.Marshal(map[string]any{"Component": "run", "DBPath": oldPath, "Raw": true})
				must(t, err)
				inspect := exec.Command(legacy)
				inspect.Stdin = bytes.NewReader(payload)
				raw, err = inspect.Output()
				must(t, err)
			} else if err != nil {
				t.Fatalf("legacy: %v\n%s", err, stderr.String())
			}
			var before probeResult
			must(t, json.Unmarshal(raw, &before))
			rt, key := runParityRuntime(t, db, "", true)
			request := httptest.NewRequest("PATCH", "/v1/api/agently/run", strings.NewReader(tc.input))
			request.Header.Set("Content-Type", "application/json")
			scope, err := requestprovider.New(request)
			must(t, err)
			defer scope.Close()
			out, mutationErr := rt.ExecuteRoute(context.Background(), "PATCH", "/v1/api/agently/run", scope)
			if before.Failed != tc.expect.failed || (mutationErr != nil) != tc.expect.failed {
				t.Fatalf("legacy failed=%v (%s), new=%v expected=%v", before.Failed, before.Error, mutationErr, tc.expect.failed)
			}
			value, err := rt.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "GET", Path: "/v1/api/agently/run/{id}"}}, Input: &read.RunRowsInput{}})
			must(t, err)
			raw, err = json.Marshal(value.(*read.RunRowsOutput).Data)
			must(t, err)
			var rows []json.RawMessage
			must(t, json.Unmarshal(raw, &rows))
			if !tc.expect.legacyPanic && !reflect.DeepEqual(normalizeRows(t, before.Rows), normalizeRows(t, rows)) {
				t.Fatalf("state parity\nlegacy=%s\nnew=%s", pretty(normalizeRows(t, before.Rows)), pretty(normalizeRows(t, rows)))
			}
			var status, owner string
			var attempt int
			must(t, db.QueryRow("SELECT status,lease_owner,attempt FROM run WHERE id='owned'").Scan(&status, &owner, &attempt))
			if status != tc.expect.status || owner != tc.expect.owner || attempt != tc.expect.attempt {
				t.Fatalf("state=(%s,%s,%d) expected=%+v", status, owner, attempt, tc.expect)
			}
			if !tc.expect.failed && !tc.expect.legacyPanic {
				var oldOutput, newOutput []json.RawMessage
				must(t, json.Unmarshal(before.Output, &oldOutput))
				raw, err = json.Marshal(out.(*write.Output).Data)
				must(t, err)
				must(t, json.Unmarshal(raw, &newOutput))
				if !reflect.DeepEqual(normalizeRows(t, oldOutput), normalizeRows(t, newOutput)) {
					t.Fatalf("response parity\nlegacy=%s\nnew=%s", before.Output, raw)
				}
			}
		})
	}
}
