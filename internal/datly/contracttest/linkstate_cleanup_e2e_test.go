package tests

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	current "github.com/viant/agently-core/internal/datly/oauth/linkstate/read"
	replace "github.com/viant/agently-core/internal/datly/oauth/linkstate/write"
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
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func TestLinkStateCleanupNativeContract(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type input struct {
		before string
		reject bool
	}
	type expect struct {
		failed    bool
		deleted   int
		oldest    string
		remaining []string
	}
	type useCase struct {
		desc   string
		input  input
		expect expect
	}
	for _, tc := range []useCase{
		{"boundary deletes expired and consumed", input{before: "2026-01-02 00:00:00"}, expect{deleted: 3, oldest: "2025-12-31 00:00:00", remaining: []string{"c"}}},
		{"earlier horizon", input{before: "2026-01-01 00:00:00"}, expect{deleted: 2, oldest: "2025-12-31 00:00:00", remaining: []string{"b", "c"}}},
		{"no rows before horizon", input{before: "2025-12-01 00:00:00"}, expect{remaining: []string{"a", "b", "c", "d"}}},
		{"all expired", input{before: "2026-01-03 00:00:00"}, expect{deleted: 4, oldest: "2025-12-31 00:00:00", remaining: []string{}}},
		{"missing horizon rejected", input{}, expect{failed: true, remaining: []string{"a", "b", "c", "d"}}},
		{"late deletion failure rolls back entire batch", input{before: "2026-01-02 00:00:00", reject: true}, expect{failed: true, remaining: []string{"a", "b", "c", "d"}}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			db, _ := linkStateCleanupFixture(t, project)
			if tc.input.reject {
				for _, fixture := range []*sql.DB{db} {
					_, err := fixture.Exec(`CREATE TRIGGER reject_link_cleanup BEFORE DELETE ON oauth_link_state WHEN OLD.flow_hash='b' BEGIN SELECT RAISE(ABORT,'fixture cleanup rejection'); END`)
					must(t, err)
				}
			}
			rt, readerKey := linkStateCleanupRuntime(t, db)
			rows := []map[string]any{}
			if tc.input.before != "" {
				rows = expiredLinkStateRows(t, rt, readerKey, tc.input.before)
			}
			body, err := json.Marshal(map[string]any{"data": rows})
			must(t, err)
			request := httptest.NewRequest("PATCH", "/v1/internal/agently/user/oauth/linkstate/write?mode=cleanup&before="+url.QueryEscape(tc.input.before), bytes.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			scope, err := requestprovider.New(request)
			must(t, err)
			defer scope.Close()
			result, err := rt.ExecuteRoute(context.Background(), "PATCH", "/v1/internal/agently/user/oauth/linkstate/write", scope)
			if (err != nil) != tc.expect.failed {
				t.Fatalf("native failure=%v expected=%v", err, tc.expect.failed)
			}
			if !tc.expect.failed {
				native := result.(*replace.Output)
				if native.Deleted != tc.expect.deleted || native.OldestExpiresAt != tc.expect.oldest {
					t.Fatalf("native=%+v expected=%+v", native, tc.expect)
				}
			}
			newRows := linkStateRemainingFlows(t, db)
			if !reflect.DeepEqual(newRows, tc.expect.remaining) {
				t.Fatalf("remaining=%v expected=%v", newRows, tc.expect.remaining)
			}
		})
	}
}
func linkStateCleanupFixture(t *testing.T, project string) (*sql.DB, string) {
	db, path := goalFixture(t, project)
	_, err := db.Exec(`INSERT INTO oauth_link_state(state_hash,flow_hash,user_id,session_hash,provider,expires_at,consumed_at,created_at) VALUES
 ('state-a','a','u1','s1','idp','2026-01-01 00:00:00',NULL,'2025-01-01 00:00:00'),
 ('state-b','b','u1','s2','idp','2026-01-02 00:00:00',NULL,'2025-01-01 00:00:00'),
 ('state-c','c','u1','s3','idp','2026-01-03 00:00:00',NULL,'2025-01-01 00:00:00'),
 ('state-d','d','u2','s4','idp','2025-12-31 00:00:00','2026-01-01 00:00:00','2025-01-01 00:00:00');`)
	must(t, err)
	return db, path
}
func linkStateRemainingFlows(t *testing.T, db *sql.DB) []string {
	rows, err := db.Query("SELECT flow_hash FROM oauth_link_state ORDER BY flow_hash")
	must(t, err)
	defer rows.Close()
	result := []string{}
	for rows.Next() {
		var flow string
		must(t, rows.Scan(&flow))
		result = append(result, flow)
	}
	must(t, rows.Err())
	return result
}
func expiredLinkStateRows(t *testing.T, rt *druntime.Runtime, key spec.Key, before string) []map[string]any {
	input := &current.LinkStateInput{}
	input.SetMode("expired")
	input.SetBefore(before)
	out, err := rt.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "GET", Path: "/v1/internal/agently/user/oauth/linkstate/current"}}, Input: input})
	must(t, err)
	result := []map[string]any{}
	for _, row := range out.(*current.LinkStateOutput).Data {
		result = append(result, map[string]any{"flowHash": row.FlowHash, "shouldDelete": true})
	}
	return result
}
func linkStateCleanupRuntime(t *testing.T, db *sql.DB, supplied ...*sql.Tx) (*druntime.Runtime, spec.Key) {
	resources := resource.New()
	must(t, resources.Register(current.ReaderDatlyResourceNamespace, current.ReaderDatlyResources))
	must(t, resources.Register(replace.WriteDatlyResourceNamespace, replace.WriteDatlyResources))
	ca := payloadArtifact(t, resources, reflect.TypeFor[current.ReaderComponent](), reflect.TypeFor[current.LinkStateInput](), reflect.TypeFor[current.LinkStateOutput]())
	wa := payloadArtifact(t, resources, reflect.TypeFor[replace.WriteComponent](), reflect.TypeFor[replace.Input](), reflect.TypeFor[replace.Output]())
	reader, err := ca.ReaderCompilation().NewExecution(bootstrap.ReaderRuntimeConfig{SQL: &dsql.SQLComponent{DB: db}})
	must(t, err)
	views, err := viewprovider.New(viewprovider.Config{Dependencies: wa.ViewDependencies, Input: wa.Input, SQL: &dsql.SQLComponent{DB: db}})
	must(t, err)
	handler, err := writer.New(wa.Component, reflect.TypeFor[replace.Input](), reflect.TypeFor[replace.Output](), "patch")
	must(t, err)
	rt, err := druntime.NewRuntime([]*registry.RegisteredComponent{{Component: ca.Component, Input: ca.Input, Output: ca.Output, OutputType: reflect.TypeFor[current.LinkStateOutput](), Reader: reader, Providers: []locator.Provider{ordinaryAccess("linkstateaccess", func(context.Context, reflect.Type, string) (any, bool, error) { return true, true, nil })}}, {Component: wa.Component, Input: wa.Input, Output: wa.Output, OutputType: reflect.TypeFor[replace.Output](), Handler: handler, Providers: []locator.Provider{views}, DataSource: dml.Source{DB: db, Tx: cleanupCallerTx(supplied)}}}, druntime.WithResources(resources))
	must(t, err)
	return rt, ca.Component.Key
}

func cleanupCallerTx(supplied []*sql.Tx) *sql.Tx {
	if len(supplied) == 0 {
		return nil
	}
	return supplied[0]
}

func TestLinkStateCleanupRechecksChangedRows(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type input struct{ vanish bool }
	type expect struct{ remaining []string }
	type useCase struct {
		desc   string
		input  input
		expect expect
	}
	for _, tc := range []useCase{{"expiry extended after selection", input{}, expect{remaining: []string{"a", "c"}}}, {"row vanished after selection", input{vanish: true}, expect{remaining: []string{"c"}}}} {
		t.Run(tc.desc, func(t *testing.T) {
			db, _ := linkStateCleanupFixture(t, project)
			rt, key := linkStateCleanupRuntime(t, db)
			before := "2026-01-02 00:00:00"
			rows := expiredLinkStateRows(t, rt, key, before)
			if tc.input.vanish {
				_, err := db.Exec("DELETE FROM oauth_link_state WHERE flow_hash='a'")
				must(t, err)
			} else {
				_, err := db.Exec("UPDATE oauth_link_state SET expires_at='2027-01-01 00:00:00' WHERE flow_hash='a'")
				must(t, err)
			}
			mutate := func(rows []map[string]any) (*replace.Output, error) {
				body, err := json.Marshal(map[string]any{"data": rows})
				must(t, err)
				request := httptest.NewRequest("PATCH", "/v1/internal/agently/user/oauth/linkstate/write?mode=cleanup&before="+url.QueryEscape(before), bytes.NewReader(body))
				request.Header.Set("Content-Type", "application/json")
				scope, err := requestprovider.New(request)
				must(t, err)
				defer scope.Close()
				out, err := rt.ExecuteRoute(context.Background(), "PATCH", "/v1/internal/agently/user/oauth/linkstate/write", scope)
				if err != nil {
					return nil, err
				}
				return out.(*replace.Output), nil
			}
			if _, err := mutate(rows); err == nil {
				t.Fatal("stale selected batch was accepted")
			}
			current := linkStateRemainingFlows(t, db)
			expectedBefore := []string{"a", "b", "c", "d"}
			if tc.input.vanish {
				expectedBefore = []string{"b", "c", "d"}
			}
			if !reflect.DeepEqual(current, expectedBefore) {
				t.Fatalf("partial deletion after failed batch: %v", current)
			}
			fresh := expiredLinkStateRows(t, rt, key, before)
			out, err := mutate(fresh)
			must(t, err)
			if out.Deleted != 2 || out.OldestExpiresAt != "2025-12-31 00:00:00" {
				t.Fatalf("retry result=%+v", out)
			}
			if actual := linkStateRemainingFlows(t, db); !reflect.DeepEqual(actual, tc.expect.remaining) {
				t.Fatalf("remaining=%v expected=%v", actual, tc.expect.remaining)
			}
		})
	}
}

func TestLinkStateCleanupCallerTransaction(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type useCase struct {
		desc   string
		commit bool
		expect []string
	}
	for _, tc := range []useCase{{"caller rollback restores all rows", false, []string{"a", "b", "c", "d"}}, {"caller commit keeps deletions", true, []string{"c"}}} {
		t.Run(tc.desc, func(t *testing.T) {
			db, _ := linkStateCleanupFixture(t, project)
			before := "2026-01-02 00:00:00"
			readRT, key := linkStateCleanupRuntime(t, db)
			rows := expiredLinkStateRows(t, readRT, key, before)
			tx, err := db.BeginTx(context.Background(), nil)
			must(t, err)
			defer tx.Rollback()
			rt, _ := linkStateCleanupRuntime(t, db, tx)
			body, err := json.Marshal(map[string]any{"data": rows})
			must(t, err)
			request := httptest.NewRequest("PATCH", "/v1/internal/agently/user/oauth/linkstate/write?mode=cleanup&before="+url.QueryEscape(before), bytes.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			scope, err := requestprovider.New(request)
			must(t, err)
			defer scope.Close()
			result, err := rt.ExecuteRoute(context.Background(), "PATCH", "/v1/internal/agently/user/oauth/linkstate/write", scope)
			must(t, err)
			out := result.(*replace.Output)
			if out.Deleted != 0 || out.OldestExpiresAt != "" {
				t.Fatalf("caller-pending write was reported as committed: %+v", out)
			}
			var pending int
			must(t, tx.QueryRow("SELECT COUNT(*) FROM oauth_link_state").Scan(&pending))
			if pending != 1 {
				t.Fatalf("pending rows=%d", pending)
			}
			if tc.commit {
				must(t, tx.Commit())
			} else {
				must(t, tx.Rollback())
			}
			if actual := linkStateRemainingFlows(t, db); !reflect.DeepEqual(actual, tc.expect) {
				t.Fatalf("rows=%v expected=%v", actual, tc.expect)
			}
		})
	}
}
