package tests

import (
	"bytes"
	"context"
	"database/sql"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	read "github.com/viant/agently-core/internal/datly/toolexecutionclaim/read"
	write "github.com/viant/agently-core/internal/datly/toolexecutionclaim/write"
	"github.com/viant/bindly/locator"
	requestprovider "github.com/viant/bindly/provider/request"
	"github.com/viant/bindly/resource"
	"github.com/viant/datly/bootstrap"
	dexec "github.com/viant/datly/exec"
	druntime "github.com/viant/datly/runtime"
	writer "github.com/viant/datly/runtime/handler/writer"
	"github.com/viant/datly/runtime/registry"
	"github.com/viant/datly/spec"
	dsql "github.com/viant/datly/sql"
	"github.com/viant/datly/sql/dml"
	viewprovider "github.com/viant/datly/sql/reader/provider"
)

func claimFixture(t *testing.T, project string) (*sql.DB, string) {
	t.Helper()
	db, path := goalFixture(t, project)
	_, err := db.Exec(`INSERT INTO tool_execution_claim(claim_key,rule_id,canonical_tool_name,turn_id,semantic_request_hash,state,created_at,updated_at) VALUES
 ('claim-a','rule-a','tool-a','t1','hash-a','completed','2026-01-01 00:00:00','2026-01-02 00:00:00'),
 ('claim-b','rule-b','tool-b','t1','hash-b','failed','2026-01-01 00:00:00','2026-01-02 00:00:00'),
 ('claim-c','rule-c','tool-c','t2','hash-c','claimed','2026-01-01 00:00:00','2026-01-02 00:00:00')`)
	must(t, err)
	return db, path
}

func claimRuntime(t *testing.T, db *sql.DB, trusted bool, supplied *sql.Tx) (*druntime.Runtime, spec.Key, spec.Key) {
	t.Helper()
	resources := resource.New()
	must(t, resources.Register(read.ReaderDatlyResourceNamespace, read.ReaderDatlyResources))
	must(t, resources.Register(write.WriterDatlyResourceNamespace, write.WriterDatlyResources))
	r := payloadArtifact(t, resources, reflect.TypeFor[read.ReaderComponent](), reflect.TypeFor[read.Input](), reflect.TypeFor[read.Output]())
	w := payloadArtifact(t, resources, reflect.TypeFor[write.WriterComponent](), reflect.TypeFor[write.Input](), reflect.TypeFor[write.Output]())
	reader, err := r.ReaderCompilation().NewExecution(bootstrap.ReaderRuntimeConfig{SQL: &dsql.SQLComponent{DB: db}})
	must(t, err)
	views, err := viewprovider.New(viewprovider.Config{Dependencies: w.ViewDependencies, Input: w.Input, SQL: &dsql.SQLComponent{DB: db}})
	must(t, err)
	handler, err := writer.New(w.Component, reflect.TypeFor[write.Input](), reflect.TypeFor[write.Output](), "patch")
	must(t, err)
	access := ordinaryAccess("claimaccess", func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
		if name == "internal" {
			return trusted, true, nil
		}
		return nil, false, nil
	})
	rt, err := druntime.NewRuntime([]*registry.RegisteredComponent{
		{Component: r.Component, Input: r.Input, Output: r.Output, OutputType: reflect.TypeFor[read.Output](), Reader: reader, Providers: []locator.Provider{access}},
		{Component: w.Component, Input: w.Input, Output: w.Output, OutputType: reflect.TypeFor[write.Output](), Handler: handler, Providers: []locator.Provider{access, views}, DataSource: dml.Source{DB: db, Tx: supplied}},
	}, druntime.WithResources(resources))
	must(t, err)
	return rt, r.Component.Key, w.Component.Key
}

func claimRows(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query("SELECT claim_key FROM tool_execution_claim ORDER BY claim_key")
	must(t, err)
	defer rows.Close()
	result := []string{}
	for rows.Next() {
		var key string
		must(t, rows.Scan(&key))
		result = append(result, key)
	}
	must(t, rows.Err())
	return result
}

func TestToolExecutionClaimDeleteLegacySQLParity(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type input struct {
		turn       string
		rejectLate bool
	}
	type expect struct {
		failure bool
		keys    []string
	}
	type useCase struct {
		desc   string
		input  input
		expect expect
	}
	for _, tc := range []useCase{
		{"delete claims by turn", input{turn: "t1"}, expect{keys: []string{"claim-c"}}},
		{"unknown turn is a no-op", input{turn: "missing"}, expect{keys: []string{"claim-a", "claim-b", "claim-c"}}},
		{"late delete failure rolls back entire batch", input{turn: "t1", rejectLate: true}, expect{failure: true, keys: []string{"claim-a", "claim-b", "claim-c"}}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			oldDB, _ := claimFixture(t, project)
			db, _ := claimFixture(t, project)
			if tc.input.rejectLate {
				for _, fixture := range []*sql.DB{oldDB, db} {
					_, err := fixture.Exec("CREATE TRIGGER reject_claim_b BEFORE DELETE ON tool_execution_claim WHEN OLD.claim_key='claim-b' BEGIN SELECT RAISE(ABORT,'fixture late claim rejection'); END")
					must(t, err)
				}
			}
			// This is the exact legacy deletion predicate from service_delete_tree.go,
			// confined to the independent comparison fixture.
			_, oldErr := oldDB.Exec("DELETE FROM tool_execution_claim WHERE turn_id = ?", tc.input.turn)
			rt, readerKey, writerKey := claimRuntime(t, db, true, nil)
			query := &read.Input{}
			query.SetTurnID(tc.input.turn)
			value, err := rt.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: readerKey, Route: spec.RouteRef{Method: "GET", Path: "/v1/internal/agently/tool-execution-claim"}}, Input: query})
			must(t, err)
			claims := make([]*write.Claim, 0, len(value.(*read.Output).Data))
			for _, row := range value.(*read.Output).Data {
				claim := &write.Claim{}
				claim.SetClaimKey(row.ClaimKey)
				claim.SetShouldDelete(true)
				claims = append(claims, claim)
			}
			mutation := &write.Input{}
			mutation.SetExpectedTurnID(tc.input.turn)
			mutation.SetClaims(claims)
			_, err = rt.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: writerKey, Route: spec.RouteRef{Method: "PATCH", Path: "/v1/internal/agently/tool-execution-claim"}}, Input: mutation})
			if (oldErr != nil) != tc.expect.failure || (err != nil) != tc.expect.failure {
				t.Fatalf("legacy error=%v, native error=%v, expected failure=%v", oldErr, err, tc.expect.failure)
			}
			oldRows, newRows := claimRows(t, oldDB), claimRows(t, db)
			if !reflect.DeepEqual(oldRows, newRows) || !reflect.DeepEqual(newRows, tc.expect.keys) {
				t.Fatalf("legacy=%v native=%v expected=%v", oldRows, newRows, tc.expect.keys)
			}
		})
	}
}

func TestToolExecutionClaimGuardedDelete(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type input struct {
		key, expectedTurn string
		trusted           bool
	}
	type expect struct {
		failure bool
		keys    []string
	}
	type useCase struct {
		desc   string
		input  input
		expect expect
	}
	for _, tc := range []useCase{
		{"matching turn deletes", input{key: "claim-a", expectedTurn: "t1", trusted: true}, expect{keys: []string{"claim-b", "claim-c"}}},
		{"different turn cannot delete", input{key: "claim-a", expectedTurn: "t2", trusted: true}, expect{failure: true, keys: []string{"claim-a", "claim-b", "claim-c"}}},
		{"missing expected turn rejected", input{key: "claim-a", trusted: true}, expect{failure: true, keys: []string{"claim-a", "claim-b", "claim-c"}}},
		{"untrusted caller rejected", input{key: "claim-a", expectedTurn: "t1"}, expect{failure: true, keys: []string{"claim-a", "claim-b", "claim-c"}}},
		{"vanished selected claim fails active guard", input{key: "missing", expectedTurn: "t1", trusted: true}, expect{failure: true, keys: []string{"claim-a", "claim-b", "claim-c"}}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			db, _ := claimFixture(t, project)
			rt, _, key := claimRuntime(t, db, tc.input.trusted, nil)
			claim := &write.Claim{}
			claim.SetClaimKey(tc.input.key)
			claim.SetShouldDelete(true)
			mutation := &write.Input{}
			if tc.input.expectedTurn != "" {
				mutation.SetExpectedTurnID(tc.input.expectedTurn)
			}
			mutation.SetClaims([]*write.Claim{claim})
			_, err := rt.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "PATCH", Path: "/v1/internal/agently/tool-execution-claim"}}, Input: mutation})
			if (err != nil) != tc.expect.failure {
				t.Fatalf("failure=%v, expected=%v", err, tc.expect.failure)
			}
			if rows := claimRows(t, db); !reflect.DeepEqual(rows, tc.expect.keys) {
				t.Fatalf("rows=%v expected=%v", rows, tc.expect.keys)
			}
		})
	}
}

func TestToolExecutionClaimReaderPredicates(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type input struct {
		turn, state, key string
		trusted          bool
	}
	type useCase struct {
		desc   string
		input  input
		expect []string
	}
	for _, tc := range []useCase{
		{"turn filter", input{turn: "t1", trusted: true}, []string{"claim-a", "claim-b"}},
		{"state and turn intersect", input{turn: "t1", state: "failed", trusted: true}, []string{"claim-b"}},
		{"claim key filter", input{key: "claim-c", trusted: true}, []string{"claim-c"}},
		{"unknown key", input{key: "missing", trusted: true}, []string{}},
		{"host denial cannot list claims", input{turn: "t1"}, []string{}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			db, _ := claimFixture(t, project)
			rt, key, _ := claimRuntime(t, db, tc.input.trusted, nil)
			query := &read.Input{}
			if tc.input.turn != "" {
				query.SetTurnID(tc.input.turn)
			}
			if tc.input.state != "" {
				query.SetState(tc.input.state)
			}
			if tc.input.key != "" {
				query.SetClaimKey(tc.input.key)
			}
			value, err := rt.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "GET", Path: "/v1/internal/agently/tool-execution-claim"}}, Input: query})
			must(t, err)
			keys := []string{}
			for _, row := range value.(*read.Output).Data {
				keys = append(keys, row.ClaimKey)
			}
			if !reflect.DeepEqual(keys, tc.expect) {
				t.Fatalf("keys=%v expected=%v", keys, tc.expect)
			}
		})
	}
}

func TestToolExecutionClaimWriterPresenceAndOwnership(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type input struct {
		key, state string
		insert     bool
	}
	type expect struct {
		failure bool
		state   string
	}
	type useCase struct {
		desc   string
		input  input
		expect expect
	}
	for _, tc := range []useCase{
		{"insert supplies timestamps", input{key: "claim-new", state: "claimed", insert: true}, expect{state: "claimed"}},
		{"sparse state patch preserves claim identity", input{key: "claim-a", state: "failed"}, expect{state: "failed"}},
		{"invalid state rejected", input{key: "claim-a", state: "impossible"}, expect{failure: true, state: "completed"}},
		{"incomplete new claim rejected", input{key: "claim-new", state: "claimed"}, expect{failure: true}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			db, _ := claimFixture(t, project)
			rt, _, key := claimRuntime(t, db, true, nil)
			claim := &write.Claim{}
			claim.SetClaimKey(tc.input.key)
			claim.SetState(tc.input.state)
			if tc.input.insert {
				claim.SetRuleId("rule-new")
				claim.SetCanonicalToolName("tool-new")
				claim.SetTurnId("t1")
				claim.SetSemanticRequestHash("hash-new")
			}
			mutation := &write.Input{}
			mutation.SetClaims([]*write.Claim{claim})
			_, err := rt.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "PATCH", Path: "/v1/internal/agently/tool-execution-claim"}}, Input: mutation})
			if (err != nil) != tc.expect.failure {
				t.Fatalf("mutation failure=%v expected=%v", err, tc.expect.failure)
			}
			var state string
			var created, updated time.Time
			err = db.QueryRow("SELECT state,created_at,updated_at FROM tool_execution_claim WHERE claim_key=?", tc.input.key).Scan(&state, &created, &updated)
			if tc.expect.state == "" {
				if err != sql.ErrNoRows {
					t.Fatalf("expected absent claim, got state=%s err=%v", state, err)
				}
				return
			}
			must(t, err)
			if state != tc.expect.state || created.IsZero() || updated.IsZero() {
				t.Fatalf("stored state/timestamps=(%q,%s,%s), want %q", state, created, updated, tc.expect.state)
			}
			if !tc.input.insert {
				var ruleID, turnID string
				must(t, db.QueryRow("SELECT rule_id,turn_id FROM tool_execution_claim WHERE claim_key=?", tc.input.key).Scan(&ruleID, &turnID))
				if ruleID != "rule-a" || turnID != "t1" {
					t.Fatalf("sparse patch changed identity: rule=%q turn=%q", ruleID, turnID)
				}
			}
		})
	}
}

func TestToolExecutionClaimHTTPCannotOverrideTrusted(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	db, _ := claimFixture(t, project)
	rt, _, _ := claimRuntime(t, db, false, nil)
	query := httptest.NewRequest("GET", "/v1/internal/agently/tool-execution-claim?claimKey=claim-a&trusted=true", nil)
	scope, err := requestprovider.New(query)
	must(t, err)
	defer scope.Close()
	value, err := rt.ExecuteRoute(context.Background(), "GET", "/v1/internal/agently/tool-execution-claim", scope)
	must(t, err)
	if len(value.(*read.Output).Data) != 0 {
		t.Fatal("HTTP query replaced trusted reader capability")
	}
	mutation := httptest.NewRequest("PATCH", "/v1/internal/agently/tool-execution-claim?expectedTurnId=t1&trusted=true", bytes.NewBufferString(`{"data":[{"claimKey":"claim-a","shouldDelete":true}]}`))
	mutation.Header.Set("Content-Type", "application/json")
	writeScope, err := requestprovider.New(mutation)
	must(t, err)
	defer writeScope.Close()
	if _, err := rt.ExecuteRoute(context.Background(), "PATCH", "/v1/internal/agently/tool-execution-claim", writeScope); err == nil {
		t.Fatal("HTTP query replaced trusted writer capability")
	}
	if rows := claimRows(t, db); !reflect.DeepEqual(rows, []string{"claim-a", "claim-b", "claim-c"}) {
		t.Fatalf("untrusted request mutated claims: %v", rows)
	}
}

func TestToolExecutionClaimCallerTransaction(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type useCase struct {
		desc   string
		commit bool
		expect string
	}
	for _, tc := range []useCase{{"caller rollback", false, "completed"}, {"caller commit", true, "failed"}} {
		t.Run(tc.desc, func(t *testing.T) {
			db, _ := claimFixture(t, project)
			db.SetMaxOpenConns(1)
			tx, err := db.BeginTx(context.Background(), nil)
			must(t, err)
			defer tx.Rollback()
			rt, _, key := claimRuntime(t, db, true, tx)
			claim := &write.Claim{}
			claim.SetClaimKey("claim-a")
			claim.SetState("failed")
			input := &write.Input{}
			input.SetClaims([]*write.Claim{claim})
			_, err = rt.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "PATCH", Path: "/v1/internal/agently/tool-execution-claim"}}, Input: input})
			must(t, err)
			var pending string
			must(t, tx.QueryRow("SELECT state FROM tool_execution_claim WHERE claim_key='claim-a'").Scan(&pending))
			if pending != "failed" {
				t.Fatalf("pending state=%q", pending)
			}
			if tc.commit {
				must(t, tx.Commit())
			} else {
				must(t, tx.Rollback())
			}
			var stored string
			must(t, db.QueryRow("SELECT state FROM tool_execution_claim WHERE claim_key='claim-a'").Scan(&stored))
			if stored != tc.expect {
				t.Fatalf("stored state=%q expected=%q", stored, tc.expect)
			}
		})
	}
}
