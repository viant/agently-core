package tests

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	current "github.com/viant/agently-core/internal/datly/oauth/linkstate/read"
	write "github.com/viant/agently-core/internal/datly/oauth/linkstate/write"
	"github.com/viant/bindly/locator"
	requestprovider "github.com/viant/bindly/provider/request"
	"github.com/viant/bindly/resource"
	"github.com/viant/datly/bootstrap"
	druntime "github.com/viant/datly/runtime"
	writer "github.com/viant/datly/runtime/handler/writer"
	"github.com/viant/datly/runtime/registry"
	dsql "github.com/viant/datly/sql"
	"github.com/viant/datly/sql/dml"
	viewprovider "github.com/viant/datly/sql/reader/provider"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestLinkStateConsumeLegacyParity(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	legacy := legacyProbeBinary(t, project)
	type input struct{ state, user, session, flow, expires, consumed, now string }
	type expect struct {
		failed, changed bool
		outcome         string
		legacyOutcome   string
	}
	type useCase struct {
		desc   string
		input  input
		expect expect
	}
	for _, tc := range []useCase{
		{"pending state consumed once", input{}, expect{outcome: "consumed", changed: true}},
		{"already consumed wins", input{consumed: "2026-01-01 00:00:00"}, expect{outcome: "already_consumed"}},
		{"expired state", input{expires: "2026-01-01 00:00:00"}, expect{outcome: "expired"}},
		{"wrong user", input{user: "u2"}, expect{outcome: "user_mismatch"}},
		{"wrong session", input{session: "other"}, expect{outcome: "session_mismatch"}},
		{"user mismatch precedes session mismatch", input{user: "u2", session: "other"}, expect{outcome: "user_mismatch"}},
		{"expiry precedes owner mismatch", input{expires: "2026-01-01 00:00:00", user: "u2"}, expect{outcome: "expired"}},
		{"already consumed precedes expiry", input{consumed: "2026-01-01 00:00:00", expires: "2026-01-01 00:00:00"}, expect{outcome: "already_consumed"}},
		{"expiry boundary is closed", input{expires: "2026-01-02 00:00:00"}, expect{outcome: "expired", legacyOutcome: "already_consumed"}},
		{"absent state", input{state: "missing", flow: "missing"}, expect{outcome: "absent"}},
		{"state hash mismatched flow", input{state: "other"}, expect{outcome: "absent"}},
		{"blank user rejected", input{user: " "}, expect{failed: true}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			seed := func() (*sql.DB, string) {
				db, path := goalFixture(t, project)
				expiry := tc.input.expires
				if expiry == "" {
					expiry = "2027-01-01 00:00:00"
				}
				var consumed any
				if tc.input.consumed != "" {
					consumed = tc.input.consumed
				}
				_, err := db.Exec(`INSERT INTO oauth_link_state(state_hash,flow_hash,user_id,session_hash,provider,expires_at,consumed_at,created_at) VALUES(?,?,?,?,?,?,?,'2026-01-01 00:00:00')`, "state1", "flow1", "u1", "session1", "idp", expiry, consumed)
				must(t, err)
				return db, path
			}
			oldDB, path := seed()
			db, _ := seed()
			state := tc.input.state
			if state == "" {
				state = "state1"
			}
			user := tc.input.user
			if user == "" {
				user = "u1"
			}
			session := tc.input.session
			if session == "" {
				session = "session1"
			}
			flow := tc.input.flow
			if flow == "" {
				flow = "flow1"
			}
			now := tc.input.now
			if now == "" {
				now = "2026-01-02 00:00:00"
			}
			oldBody, err := json.Marshal(map[string]any{"data": map[string]any{"stateHash": state, "canonicalUserId": user, "sessionHash": session, "now": now}})
			must(t, err)
			payload, err := json.Marshal(map[string]any{"Component": "linkStateConsume", "DBPath": path, "Body": string(oldBody)})
			must(t, err)
			cmd := exec.Command(legacy)
			cmd.Stdin = bytes.NewReader(payload)
			raw, err := cmd.Output()
			must(t, err)
			var before probeResult
			must(t, json.Unmarshal(raw, &before))
			body, err := json.Marshal(map[string]any{"data": []map[string]any{{"stateHash": state, "flowHash": flow, "userId": user, "sessionHash": session, "now": now}}})
			must(t, err)
			req := httptest.NewRequest("PATCH", "/v1/internal/agently/user/oauth/linkstate/write?mode=consume", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			scope, err := requestprovider.New(req)
			must(t, err)
			defer scope.Close()
			out, err := linkStateConsumeRuntime(t, db).ExecuteRoute(context.Background(), "PATCH", "/v1/internal/agently/user/oauth/linkstate/write", scope)
			if before.Failed != tc.expect.failed || (err != nil) != tc.expect.failed {
				t.Fatalf("legacy failure=%v (%s), native=%v expected=%v", before.Failed, before.Error, err, tc.expect.failed)
			}
			if !tc.expect.failed {
				var oldResult map[string]any
				must(t, json.Unmarshal(before.Output, &oldResult))
				legacyOutcome := tc.expect.legacyOutcome
				if legacyOutcome == "" {
					legacyOutcome = tc.expect.outcome
				}
				if oldResult["outcome"] != legacyOutcome || out.(*write.Output).Outcome != tc.expect.outcome {
					t.Fatalf("legacy=%v native=%q expected=%q", oldResult, out.(*write.Output).Outcome, tc.expect.outcome)
				}
				if tc.expect.outcome != "consumed" && out.(*write.Output).Data != nil {
					t.Fatal("rejected consume exposed state data")
				}
			}
			var oldTime, newTime sql.NullString
			must(t, oldDB.QueryRow("SELECT consumed_at FROM oauth_link_state WHERE state_hash='state1'").Scan(&oldTime))
			must(t, db.QueryRow("SELECT consumed_at FROM oauth_link_state WHERE state_hash='state1'").Scan(&newTime))
			if oldTime.Valid != newTime.Valid || oldTime.String != newTime.String {
				t.Fatalf("persisted timestamp legacy=%v native=%v", oldTime, newTime)
			}
		})
	}
}
func linkStateConsumeRuntime(t *testing.T, db *sql.DB) *druntime.Runtime {
	resources := resource.New()
	must(t, resources.Register(current.ReaderDatlyResourceNamespace, current.ReaderDatlyResources))
	must(t, resources.Register(write.WriteDatlyResourceNamespace, write.WriteDatlyResources))
	ca := payloadArtifact(t, resources, reflect.TypeFor[current.ReaderComponent](), reflect.TypeFor[current.LinkStateInput](), reflect.TypeFor[current.LinkStateOutput]())
	wa := payloadArtifact(t, resources, reflect.TypeFor[write.WriteComponent](), reflect.TypeFor[write.Input](), reflect.TypeFor[write.Output]())
	reader, err := ca.ReaderCompilation().NewExecution(bootstrap.ReaderRuntimeConfig{SQL: &dsql.SQLComponent{DB: db}})
	must(t, err)
	views, err := viewprovider.New(viewprovider.Config{Dependencies: wa.ViewDependencies, Input: wa.Input, SQL: &dsql.SQLComponent{DB: db}})
	must(t, err)
	handler, err := writer.New(wa.Component, reflect.TypeFor[write.Input](), reflect.TypeFor[write.Output](), "patch")
	must(t, err)
	rt, err := druntime.NewRuntime([]*registry.RegisteredComponent{{Component: ca.Component, Input: ca.Input, Output: ca.Output, OutputType: reflect.TypeFor[current.LinkStateOutput](), Reader: reader, Providers: []locator.Provider{ordinaryAccess("linkstateaccess", func(context.Context, reflect.Type, string) (any, bool, error) { return true, true, nil })}}, {Component: wa.Component, Input: wa.Input, Output: wa.Output, OutputType: reflect.TypeFor[write.Output](), Handler: handler, Providers: []locator.Provider{views}, DataSource: dml.Source{DB: db}}}, druntime.WithResources(resources))
	must(t, err)
	return rt
}

var _ = strings.TrimSpace

func TestLinkStateConsumeAcrossIndependentConnections(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type input struct{ callers int }
	type expect struct{ consumed int }
	type useCase struct {
		desc   string
		input  input
		expect expect
	}
	for _, tc := range []useCase{{"two consumers race for one state", input{callers: 2}, expect{consumed: 1}}} {
		t.Run(tc.desc, func(t *testing.T) {
			db, path := goalFixture(t, project)
			_, err := db.Exec(`PRAGMA journal_mode=WAL;PRAGMA busy_timeout=5000;
 INSERT INTO oauth_link_state(state_hash,flow_hash,user_id,session_hash,provider,expires_at,consumed_at,created_at) VALUES('state1','flow1','u1','session1','idp','2027-01-01 00:00:00',NULL,'2026-01-01 00:00:00')`)
			must(t, err)
			other, err := sql.Open("sqlite3", path+"?_foreign_keys=on")
			must(t, err)
			defer other.Close()
			other.SetMaxOpenConns(1)
			_, err = other.Exec("PRAGMA busy_timeout=5000")
			must(t, err)
			runtimes := []*druntime.Runtime{linkStateConsumeRuntime(t, db), linkStateConsumeRuntime(t, other)}
			type result struct {
				outcome string
				err     error
			}
			results := make([]result, tc.input.callers)
			start := make(chan struct{})
			var wg sync.WaitGroup
			for i := 0; i < tc.input.callers; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					<-start
					body := `{"data":[{"stateHash":"state1","flowHash":"flow1","userId":"u1","sessionHash":"session1","now":"2026-01-02 00:00:00"}]}`
					request := httptest.NewRequest("PATCH", "/v1/internal/agently/user/oauth/linkstate/write?mode=consume", strings.NewReader(body))
					request.Header.Set("Content-Type", "application/json")
					scope, e := requestprovider.New(request)
					if e != nil {
						results[i].err = e
						return
					}
					defer scope.Close()
					value, e := runtimes[i].ExecuteRoute(context.Background(), "PATCH", "/v1/internal/agently/user/oauth/linkstate/write", scope)
					results[i].err = e
					if e == nil {
						results[i].outcome = value.(*write.Output).Outcome
					}
				}(i)
			}
			close(start)
			wg.Wait()
			successes := 0
			for _, result := range results {
				if result.outcome == "consumed" {
					successes++
				}
			}
			if successes != tc.expect.consumed {
				t.Fatalf("consumed=%d expected=%d results=%+v", successes, tc.expect.consumed, results)
			}
			var persisted sql.NullString
			must(t, db.QueryRow("SELECT consumed_at FROM oauth_link_state WHERE state_hash='state1'").Scan(&persisted))
			if !persisted.Valid || persisted.String == "" {
				t.Fatal("single-use state was not persisted")
			}
		})
	}
}
