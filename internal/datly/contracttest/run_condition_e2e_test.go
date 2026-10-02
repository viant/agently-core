package tests

import (
	"context"
	requestprovider "github.com/viant/bindly/provider/request"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRunConditionLegacyV1Parity(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type expect struct {
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
		{"guarded and ordinary rows share a batch", `{"data":[{"id":"owned","status":"succeeded","condition":{"leaseOwner":"owner-a"}},{"id":"other","errorMessage":"changed"}]}`, expect{status: "succeeded", owner: "owner-a", attempt: 1}},
		{"unsupported conditional column is rejected", `{"data":[{"id":"owned","workerId":"new","condition":{"leaseOwner":"owner-a"}}]}`, expect{failed: true, status: "running", owner: "owner-a", attempt: 1}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			db, _ := runParityFixture(t, project)
			_, err := db.Exec("UPDATE run SET lease_owner=CASE WHEN id='owned' THEN 'owner-a' ELSE 'owner-other' END WHERE id IN ('owned','other')")
			must(t, err)
			rt, _ := runParityRuntime(t, db, "", true)
			request := httptest.NewRequest("PATCH", "/v1/api/agently/run", strings.NewReader(tc.input))
			request.Header.Set("Content-Type", "application/json")
			scope, err := requestprovider.New(request)
			must(t, err)
			defer scope.Close()
			_, mutationErr := rt.ExecuteRoute(context.Background(), "PATCH", "/v1/api/agently/run", scope)
			if (mutationErr != nil) != tc.expect.failed {
				t.Fatalf("mutation error=%v expected failure=%v", mutationErr, tc.expect.failed)
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
