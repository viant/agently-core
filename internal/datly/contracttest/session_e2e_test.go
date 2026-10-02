package tests

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	sessionread "github.com/viant/agently-core/internal/datly/session/read"
	sessionwrite "github.com/viant/agently-core/internal/datly/session/write"
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

func TestSessionLegacyV1Parity(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	legacy := legacyProbeBinary(t, project)
	type input struct {
		body    string
		filters map[string]any
	}
	type expect struct {
		failed bool
		ids    []string
		fields map[string]map[string]any
	}
	type useCase struct {
		desc   string
		input  input
		expect expect
	}
	insertion := `{"id":"s-new","userId":"u3","provider":"local","expiresAt":"2027-01-01T00:00:00Z"}`
	for _, tc := range []useCase{
		{desc: "internal session reader returns existing records", expect: expect{ids: []string{"s1", "s2"}}},
		{desc: "ID predicate selects one session", input: input{filters: map[string]any{"id": "s1"}}, expect: expect{ids: []string{"s1"}}},
		{desc: "unknown ID produces empty read", input: input{filters: map[string]any{"id": "absent"}}, expect: expect{ids: []string{}}},
		{desc: "new session receives server creation time", input: input{body: `{"data":` + insertion + `}`}, expect: expect{ids: []string{"s-new", "s1", "s2"}, fields: map[string]map[string]any{"s-new": {"userid": "u3", "provider": "local", "createdat": "<generated timestamp>", "updatedat": nil}}}},
		{desc: "new creation time deliberately replaces supplied time", input: input{body: `{"data":{"id":"s-new","userId":"u3","provider":"local","createdAt":"2026-01-01T00:00:00Z","expiresAt":"2027-01-01T00:00:00Z"}}`}, expect: expect{ids: []string{"s-new", "s1", "s2"}, fields: map[string]map[string]any{"s-new": {"createdat": "<generated timestamp>"}}}},
		{desc: "existing update marks legacy replacement fields", input: input{body: `{"data":{"id":"s1","userId":"new-owner","provider":"external","expiresAt":"2028-01-01T00:00:00Z"}}`}, expect: expect{fields: map[string]map[string]any{"s1": {"userid": "new-owner", "provider": "external", "expiresat": "2028-01-01T00:00:00Z", "createdat": "2026-01-01T00:00:00Z", "updatedat": "<generated timestamp>"}}}},
		{desc: "omitted replacement values retain legacy forced markers", input: input{body: `{"data":{"id":"s1"}}`}, expect: expect{fields: map[string]map[string]any{"s1": {"userid": "", "provider": "", "expiresat": "0001-01-01T00:00:00Z", "createdat": "2026-01-01T00:00:00Z"}}}},
		{desc: "provided update timestamp stays authoritative", input: input{body: `{"data":{"id":"s1","userId":"u1","provider":"local","expiresAt":"2027-01-01T00:00:00Z","updatedAt":"2026-01-01T00:00:00Z"}}`}, expect: expect{fields: map[string]map[string]any{"s1": {"updatedat": "2026-01-01T00:00:00Z"}}}},
		{desc: "explicit null update timestamp receives server time", input: input{body: `{"data":{"id":"s1","userId":"u1","provider":"local","expiresAt":"2027-01-01T00:00:00Z","updatedAt":null}}`}, expect: expect{fields: map[string]map[string]any{"s1": {"updatedat": "<generated timestamp>"}}}},
		{desc: "nil session body is a no-op", input: input{body: `{"data":null}`}, expect: expect{}},
		{desc: "database insert rejection preserves existing sessions", input: input{body: `{"data":{"id":"s-reject","userId":"u3","provider":"local","expiresAt":"2027-01-01T00:00:00Z"}}`}, expect: expect{failed: true}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			_, oldPath := sessionParityFixture(t, project)
			db, _ := sessionParityFixture(t, project)
			filters := tc.input.filters
			if filters == nil {
				filters = map[string]any{}
			}
			probe := struct {
				Component, DBPath, Body string
				Filters                 map[string]any
			}{"session", oldPath, tc.input.body, filters}
			payload, err := json.Marshal(probe)
			must(t, err)
			process := exec.Command(legacy)
			process.Stdin = bytes.NewReader(payload)
			var stderr bytes.Buffer
			process.Stderr = &stderr
			raw, err := process.Output()
			if err != nil {
				t.Fatalf("legacy execution: %v\n%s", err, raw)
			}
			var before probeResult
			if err := json.Unmarshal(raw, &before); err != nil {
				t.Fatalf("legacy response: %v\nstdout=%s\nstderr=%s", err, raw, stderr.String())
			}
			rt, key := sessionParityRuntime(t, db)
			var afterOutput json.RawMessage
			var mutationError error
			if tc.input.body != "" {
				var envelope struct {
					Data json.RawMessage `json:"data"`
				}
				must(t, json.Unmarshal([]byte(tc.input.body), &envelope))
				canonicalBody, err := json.Marshal(map[string]any{"data": []json.RawMessage{envelope.Data}})
				must(t, err)
				if string(envelope.Data) == "null" {
					canonicalBody = []byte(`{"data":null}`)
				}
				req := httptest.NewRequest("PATCH", "/v1/api/agently/user/session", strings.NewReader(string(canonicalBody)))
				req.Header.Set("Content-Type", "application/json")
				scope, err := requestprovider.New(req)
				must(t, err)
				defer scope.Close()
				result, err := rt.ExecuteRoute(context.Background(), "PATCH", "/v1/api/agently/user/session", scope)
				mutationError = err
				if err == nil {
					var row *sessionwrite.Session
					if len(result.(*sessionwrite.Output).Data) != 0 {
						row = result.(*sessionwrite.Output).Data[0]
					}
					afterOutput, err = json.Marshal(row)
					must(t, err)
				}
			}
			if before.Failed != tc.expect.failed || (mutationError != nil) != tc.expect.failed {
				t.Fatalf("outcome legacy=%v (%s) v1=%v expected failure=%v", before.Failed, before.Error, mutationError, tc.expect.failed)
			}
			readerInput := &sessionread.SessionInput{Has: &sessionread.SessionInputHas{}}
			if value, present := filters["id"]; present {
				encoded, err := json.Marshal(value)
				must(t, err)
				must(t, json.Unmarshal(encoded, &readerInput.Id))
				readerInput.Has.Id = true
			}
			output, err := rt.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "GET", Path: "/v1/api/agently/user/session"}}, Input: readerInput})
			must(t, err)
			data, err := json.Marshal(output.(*sessionread.SessionOutput).Data)
			must(t, err)
			var rows []json.RawMessage
			must(t, json.Unmarshal(data, &rows))
			oldRows, newRows := normalizeRows(t, before.Rows), normalizeRows(t, rows)
			// The v1 reader also carries user display identity for the root auth
			// service. Compare the original session columns with SDK0 separately.
			for _, row := range newRows {
				delete(row, "username")
				delete(row, "displayname")
				delete(row, "email")
				delete(row, "subject")
			}
			if !reflect.DeepEqual(oldRows, newRows) {
				t.Fatalf("stored row parity\nlegacy=%s\nv1=%s", pretty(oldRows), pretty(newRows))
			}
			ids := []string{}
			indexed := map[string]map[string]any{}
			for _, row := range newRows {
				id := row["id"].(string)
				ids = append(ids, id)
				indexed[id] = row
			}
			expectedIDs := tc.expect.ids
			if expectedIDs == nil {
				expectedIDs = []string{"s1", "s2"}
			}
			if !reflect.DeepEqual(ids, expectedIDs) {
				t.Fatalf("IDs=%v expected=%v", ids, expectedIDs)
			}
			for id, fields := range tc.expect.fields {
				for field, value := range fields {
					if !reflect.DeepEqual(indexed[id][field], value) {
						t.Errorf("%s.%s=%v expected=%v", id, field, indexed[id][field], value)
					}
				}
			}
			if tc.input.body != "" && !tc.expect.failed {
				old := []json.RawMessage{before.Output}
				new := []json.RawMessage{afterOutput}
				if !reflect.DeepEqual(normalizeRowsInOrder(t, old), normalizeRowsInOrder(t, new)) {
					t.Fatalf("write response parity\nlegacy=%s\nv1=%s", before.Output, afterOutput)
				}
			}
		})
	}
}

func TestSessionReaderLoadsFriendlyUserIdentity(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	db, _ := sessionParityFixture(t, project)
	_, err := db.Exec(`INSERT INTO users(id,username,display_name,email,provider,subject)
		VALUES('u1','localuser','Local User','user@example.test','oauth','provider-subject')`)
	must(t, err)
	rt, key := sessionParityRuntime(t, db)
	input := &sessionread.SessionInput{}
	input.SetId("s1")
	value, err := rt.InvokeComponent(context.Background(), dexec.ComponentRequest{
		Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "GET", Path: "/v1/api/agently/user/session"}},
		Input:  input,
	})
	must(t, err)
	rows := value.(*sessionread.SessionOutput).Data
	if len(rows) != 1 || rows[0].DisplayName == nil || *rows[0].DisplayName != "Local User" ||
		rows[0].Email == nil || *rows[0].Email != "user@example.test" ||
		rows[0].Subject == nil || *rows[0].Subject != "provider-subject" {
		t.Fatalf("joined user identity = %+v", rows)
	}
}

func sessionParityFixture(t *testing.T, project string) (*sql.DB, string) {
	t.Helper()
	db, path := goalFixture(t, project)
	_, err := db.Exec(`INSERT INTO session(id,user_id,provider,created_at,updated_at,expires_at)
 VALUES('s1','u1','local','2026-01-01 00:00:00','2026-01-01 00:00:00','2027-01-01 00:00:00'),
 ('s2','u2','local','2026-01-01 00:00:00',NULL,'2027-01-01 00:00:00');
 CREATE TRIGGER reject_session BEFORE INSERT ON session WHEN NEW.id='s-reject' BEGIN SELECT RAISE(ABORT,'fixture session rejection'); END;`)
	must(t, err)
	return db, path
}

func sessionParityRuntime(t *testing.T, db *sql.DB, supplied ...*sql.Tx) (*druntime.Runtime, spec.Key) {
	t.Helper()
	resources := resource.New()
	must(t, resources.Register(sessionread.ReaderDatlyResourceNamespace, sessionread.ReaderDatlyResources))
	must(t, resources.Register(sessionwrite.WriterDatlyResourceNamespace, sessionwrite.WriterDatlyResources))
	ra := payloadArtifact(t, resources, reflect.TypeFor[sessionread.ReaderComponent](), reflect.TypeFor[sessionread.SessionInput](), reflect.TypeFor[sessionread.SessionOutput]())
	wa := payloadArtifact(t, resources, reflect.TypeFor[sessionwrite.WriterComponent](), reflect.TypeFor[sessionwrite.Input](), reflect.TypeFor[sessionwrite.Output]())
	reader, err := ra.ReaderCompilation().NewExecution(bootstrap.ReaderRuntimeConfig{SQL: &dsql.SQLComponent{DB: db}})
	must(t, err)
	views, err := viewprovider.New(viewprovider.Config{Dependencies: wa.ViewDependencies, Input: wa.Input, SQL: &dsql.SQLComponent{DB: db}})
	must(t, err)
	handler, err := writer.New(wa.Component, reflect.TypeFor[sessionwrite.Input](), reflect.TypeFor[sessionwrite.Output](), "patch")
	must(t, err)
	var tx *sql.Tx
	if len(supplied) > 0 {
		tx = supplied[0]
	}
	rt, err := druntime.NewRuntime([]*registry.RegisteredComponent{
		{Component: ra.Component, Input: ra.Input, Output: ra.Output, OutputType: reflect.TypeFor[sessionread.SessionOutput](), Reader: reader},
		{Component: wa.Component, Input: wa.Input, Output: wa.Output, OutputType: reflect.TypeFor[sessionwrite.Output](), Handler: handler, Providers: []locator.Provider{views}, DataSource: dml.Source{DB: db, Tx: tx}},
	}, druntime.WithResources(resources))
	must(t, err)
	return rt, ra.Component.Key
}
