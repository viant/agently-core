package tests

import (
	"context"
	"database/sql"
	"encoding/json"
	current "github.com/viant/agently-core/internal/datly/oauth/linkstate/read"
	"github.com/viant/bindly/locator"
	"github.com/viant/bindly/provider/request"
	"github.com/viant/bindly/resource"
	"github.com/viant/datly/bootstrap"
	dexec "github.com/viant/datly/exec"
	druntime "github.com/viant/datly/runtime"
	"github.com/viant/datly/runtime/registry"
	"github.com/viant/datly/spec"
	dsql "github.com/viant/datly/sql"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"
)

func TestLinkStateCanonicalReader(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type input struct {
		flow, state string
		pending     bool
	}
	type expect struct {
		failed bool
		hashes []string
	}
	type useCase struct {
		desc   string
		input  input
		expect expect
	}
	for _, tc := range []useCase{
		{"flow lookup", input{flow: "flow1"}, expect{hashes: []string{"state1"}}},
		{"state lookup", input{state: "state1"}, expect{hashes: []string{"state1"}}},
		{"pending accepts unconsumed row", input{flow: "flow1", pending: true}, expect{hashes: []string{"state1"}}},
		{"pending excludes consumed row", input{flow: "flow2", pending: true}, expect{hashes: []string{}}},
		{"classification can read consumed row", input{state: "state2"}, expect{hashes: []string{"state2"}}},
		{"unknown state is empty", input{state: "absent"}, expect{hashes: []string{}}},
		{"bound state value cannot broaden lookup", input{state: "' OR 1=1 --"}, expect{hashes: []string{}}},
		{"empty lookup fails", input{}, expect{failed: true}},
		{"two identity kinds fail", input{flow: "flow1", state: "state2"}, expect{failed: true}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			db, _ := goalFixture(t, project)
			_, err := db.Exec(`INSERT INTO oauth_link_state(state_hash,flow_hash,user_id,session_hash,provider,expires_at,consumed_at,created_at) VALUES
 ('state1','flow1','u1','session1','idp','2027-01-01 00:00:00',NULL,'2026-01-01 00:00:00'),
 ('state2','flow2','u2','session2','idp','2027-01-01 00:00:00','2026-01-02 00:00:00','2026-01-01 00:00:00')`)
			must(t, err)
			rt, key := linkStateReaderRuntime(t, db)
			input := &current.LinkStateInput{}
			if tc.input.flow != "" {
				input.SetFlowHash(tc.input.flow)
			}
			if tc.input.state != "" {
				input.SetStateHash(tc.input.state)
			}
			input.SetPending(tc.input.pending)
			value, err := rt.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "GET", Path: "/v1/internal/agently/user/oauth/linkstate/current"}}, Input: input})
			if (err != nil) != tc.expect.failed {
				t.Fatalf("failure=%v expected=%v", err, tc.expect.failed)
			}
			if err != nil {
				return
			}
			hashes := []string{}
			for _, row := range value.(*current.LinkStateOutput).Data {
				hashes = append(hashes, row.StateHash)
				expires, err := time.Parse(time.RFC3339Nano, row.ExpiresAt)
				must(t, err)
				created, err := time.Parse(time.RFC3339Nano, row.CreatedAt)
				must(t, err)
				if !expires.Equal(time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)) || !created.Equal(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)) {
					t.Fatalf("timestamp meaning changed: expires=%q created=%q", row.ExpiresAt, row.CreatedAt)
				}
			}
			if !reflect.DeepEqual(hashes, tc.expect.hashes) {
				t.Fatalf("hashes=%v expected=%v", hashes, tc.expect.hashes)
			}
		})
	}
	t.Run("HTTP state lookup", func(t *testing.T) {
		db, _ := goalFixture(t, project)
		_, err := db.Exec(`INSERT INTO oauth_link_state VALUES('s','f','u','session','idp','2027-01-01 00:00:00',NULL,'2026-01-01 00:00:00')`)
		must(t, err)
		rt, _ := linkStateReaderRuntime(t, db)
		req := httptest.NewRequest("GET", "/v1/internal/agently/user/oauth/linkstate/current?stateHash=s", nil)
		scope, err := request.New(req)
		must(t, err)
		defer scope.Close()
		out, err := rt.ExecuteRoute(context.Background(), "GET", "/v1/internal/agently/user/oauth/linkstate/current", scope)
		must(t, err)
		raw, err := json.Marshal(out.(*current.LinkStateOutput).Data)
		must(t, err)
		var rows []map[string]any
		must(t, json.Unmarshal(raw, &rows))
		if len(rows) != 1 {
			t.Fatalf("HTTP state lookup returned %d rows, want 1", len(rows))
		}
		stateHash, present := rows[0]["stateHash"]
		if !present || stateHash != "s" {
			t.Fatalf("HTTP state lookup requires lowerCamel stateHash=s, got %s", raw)
		}
		if _, legacyName := rows[0]["StateHash"]; legacyName {
			t.Fatal("HTTP state lookup exposed PascalCase StateHash")
		}
	})
}
func linkStateReaderRuntime(t *testing.T, db *sql.DB) (*druntime.Runtime, spec.Key) {
	resources := resource.New()
	must(t, resources.Register(current.ReaderDatlyResourceNamespace, current.ReaderDatlyResources))
	artifact := payloadArtifact(t, resources, reflect.TypeFor[current.ReaderComponent](), reflect.TypeFor[current.LinkStateInput](), reflect.TypeFor[current.LinkStateOutput]())
	reader, err := artifact.ReaderCompilation().NewExecution(bootstrap.ReaderRuntimeConfig{SQL: &dsql.SQLComponent{DB: db}})
	must(t, err)
	rt, err := druntime.NewRuntime([]*registry.RegisteredComponent{{Component: artifact.Component, Input: artifact.Input, Output: artifact.Output, OutputType: reflect.TypeFor[current.LinkStateOutput](), Reader: reader, Providers: []locator.Provider{ordinaryAccess("linkstateaccess", func(context.Context, reflect.Type, string) (any, bool, error) { return true, true, nil })}}}, druntime.WithResources(resources))
	must(t, err)
	return rt, artifact.Component.Key
}

func TestLinkStatePendingReaderLegacyParity(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type useCase struct {
		desc, input string
		expect      []string
	}
	for _, tc := range []useCase{
		{"pending flow", "flow1", []string{"state1"}},
		{"consumed flow excluded", "flow2", []string{}},
		{"expiry is decided by store after the lookup", "flow3", []string{"state3"}},
		{"unknown flow", "absent", []string{}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			fixture := func() *sql.DB {
				db, _ := goalFixture(t, project)
				_, err := db.Exec(`INSERT INTO oauth_link_state(state_hash,flow_hash,user_id,session_hash,provider,expires_at,consumed_at,created_at) VALUES
 ('state1','flow1','u1','session1','idp','2027-01-01 00:00:00',NULL,'2026-01-01 00:00:00'),
 ('state2','flow2','u2','session2','idp','2027-01-01 00:00:00','2026-01-02 00:00:00','2026-01-01 00:00:00'),
 ('state3','flow3','u3','session3','idp','2025-01-01 00:00:00',NULL,'2026-01-01 00:00:00')`)
				must(t, err)
				return db
			}
			db := fixture()
			rt, key := linkStateReaderRuntime(t, db)
			input := &current.LinkStateInput{}
			input.SetFlowHash(tc.input)
			input.SetPending(true)
			out, err := rt.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "GET", Path: "/v1/internal/agently/user/oauth/linkstate/current"}}, Input: input})
			must(t, err)
			raw, err := json.Marshal(out.(*current.LinkStateOutput).Data)
			must(t, err)
			var rows []json.RawMessage
			must(t, json.Unmarshal(raw, &rows))
			newRows := normalizeRowsInOrder(t, rows)
			got := []string{}
			for _, row := range newRows {
				got = append(got, row["statehash"].(string))
			}
			if !reflect.DeepEqual(got, tc.expect) {
				t.Fatalf("hashes=%v expected=%v", got, tc.expect)
			}
		})
	}
}
