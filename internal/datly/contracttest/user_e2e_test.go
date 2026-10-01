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

	userread "github.com/viant/agently-core/internal/datly/user/read"
	userwrite "github.com/viant/agently-core/internal/datly/user/write"
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

func TestUserLegacyV1Parity(t *testing.T) {
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
	insertion := `{"id":"u-new","username":"newuser","provider":"local","timezone":"UTC"}`
	for _, tc := range []useCase{
		{desc: "unfiltered internal reader includes both users", expect: expect{ids: []string{"u1", "u2"}}},
		{desc: "ID predicate narrows to one user", input: input{filters: map[string]any{"id": "u1"}}, expect: expect{ids: []string{"u1"}}},
		{desc: "username predicate narrows to one user", input: input{filters: map[string]any{"username": "second"}}, expect: expect{ids: []string{"u2"}}},
		{desc: "ID and username predicates combine", input: input{filters: map[string]any{"id": "u1", "username": "second"}}, expect: expect{ids: []string{}}},
		{desc: "insertion supplies disabled and creation defaults", input: input{body: `{"data":[` + insertion + `]}`}, expect: expect{ids: []string{"u-new", "u1", "u2"}, fields: map[string]map[string]any{"u-new": {"disabled": float64(0), "createdat": "<generated timestamp>", "updatedat": nil}}}},
		{desc: "explicit null insertion defaults match legacy", input: input{body: `{"data":[{"id":"u-new","username":"newuser","provider":"local","timezone":"UTC","createdAt":null,"disabled":null}]}`}, expect: expect{ids: []string{"u-new", "u1", "u2"}, fields: map[string]map[string]any{"u-new": {"disabled": float64(0), "createdat": "<generated timestamp>"}}}},
		{desc: "supplied insertion defaults remain authoritative", input: input{body: `{"data":[{"id":"u-new","username":"newuser","provider":"local","timezone":"UTC","createdAt":"2026-01-01T00:00:00Z","disabled":1}]}`}, expect: expect{ids: []string{"u-new", "u1", "u2"}, fields: map[string]map[string]any{"u-new": {"disabled": float64(1), "createdat": "2026-01-01T00:00:00Z"}}}},
		{desc: "sparse display name update preserves all omitted fields", input: input{body: `{"data":[{"id":"u1","displayName":"changed"}]}`}, expect: expect{fields: map[string]map[string]any{"u1": {"displayname": "changed", "username": "first", "provider": "local", "disabled": float64(1), "haship": "seed-hash", "createdat": "2026-01-01T00:00:00Z", "updatedat": "<generated timestamp>"}}}},
		{desc: "explicit zero and null update differ from omitted values", input: input{body: `{"data":[{"id":"u1","disabled":0,"email":null,"settings":""}]}`}, expect: expect{fields: map[string]map[string]any{"u1": {"disabled": float64(0), "email": nil, "settings": ""}}}},
		{desc: "null update timestamp receives legacy replacement", input: input{body: `{"data":[{"id":"u1","updatedAt":null}]}`}, expect: expect{fields: map[string]map[string]any{"u1": {"updatedat": "<generated timestamp>"}}}},
		{desc: "supplied update timestamp remains authoritative", input: input{body: `{"data":[{"id":"u1","updatedAt":"2026-01-01T00:00:00Z"}]}`}, expect: expect{fields: map[string]map[string]any{"u1": {"updatedat": "2026-01-01T00:00:00Z"}}}},
		{desc: "mixed update and insert preserve response order", input: input{body: `{"data":[{"id":"u1","displayName":"changed"},` + insertion + `]}`}, expect: expect{ids: []string{"u-new", "u1", "u2"}}},
		{desc: "new row requires username", input: input{body: `{"data":[{"id":"u-new","provider":"local","timezone":"UTC"}]}`}, expect: expect{failed: true}},
		{desc: "existing required username cannot become empty", input: input{body: `{"data":[{"id":"u1","username":""}]}`}, expect: expect{failed: true}},
		{desc: "existing creation timestamp cannot become null", input: input{body: `{"data":[{"id":"u1","createdAt":null}]}`}, expect: expect{failed: true}},
		{desc: "duplicate username remains a database constraint", input: input{body: `{"data":[{"id":"u-new","username":"first","provider":"local","timezone":"UTC"}]}`}, expect: expect{failed: true}},
		{desc: "late database rejection rolls back earlier update", input: input{body: `{"data":[{"id":"u1","displayName":"changed"},{"id":"u-reject","username":"reject","provider":"local","timezone":"UTC"}]}`}, expect: expect{failed: true, fields: map[string]map[string]any{"u1": {"displayname": "seed name"}}}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			_, oldPath := userParityFixture(t, project)
			db, _ := userParityFixture(t, project)
			filters := tc.input.filters
			if filters == nil {
				filters = map[string]any{}
			}
			probe := struct {
				Component, DBPath, Body string
				Filters                 map[string]any
			}{"user", oldPath, tc.input.body, filters}
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
			rt, key := userParityRuntime(t, db)
			var afterOutput json.RawMessage
			var mutationError error
			if tc.input.body != "" {
				req := httptest.NewRequest("PATCH", "/v1/api/agently/user", strings.NewReader(tc.input.body))
				req.Header.Set("Content-Type", "application/json")
				scope, err := requestprovider.New(req)
				must(t, err)
				defer scope.Close()
				result, err := rt.ExecuteRoute(context.Background(), "PATCH", "/v1/api/agently/user", scope)
				mutationError = err
				if err == nil {
					afterOutput, err = json.Marshal(result.(*userwrite.Output).Data)
					must(t, err)
				}
			}
			if before.Failed != tc.expect.failed || (mutationError != nil) != tc.expect.failed {
				t.Fatalf("outcome legacy=%v (%s) v1=%v expected failure=%v", before.Failed, before.Error, mutationError, tc.expect.failed)
			}
			readerInput := &userread.UserInput{Has: &userread.UserInputHas{}}
			for name, value := range filters {
				encoded, err := json.Marshal(value)
				must(t, err)
				switch name {
				case "id":
					must(t, json.Unmarshal(encoded, &readerInput.Id))
					readerInput.Has.Id = true
				case "username":
					must(t, json.Unmarshal(encoded, &readerInput.Username))
					readerInput.Has.Username = true
				}
			}
			output, err := rt.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "GET", Path: "/v1/api/agently/user"}}, Input: readerInput})
			must(t, err)
			data, err := json.Marshal(output.(*userread.UserOutput).Data)
			must(t, err)
			var rows []json.RawMessage
			must(t, json.Unmarshal(data, &rows))
			oldRows, newRows := normalizeRows(t, before.Rows), normalizeRows(t, rows)
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
				expectedIDs = []string{"u1", "u2"}
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
				var old, new []json.RawMessage
				must(t, json.Unmarshal(before.Output, &old))
				must(t, json.Unmarshal(afterOutput, &new))
				if !reflect.DeepEqual(normalizeRowsInOrder(t, old), normalizeRowsInOrder(t, new)) {
					t.Fatalf("write response parity\nlegacy=%s\nv1=%s", before.Output, afterOutput)
				}
			}
		})
	}
}

func TestUserReaderSubjectProviderLookup(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	db, _ := userParityFixture(t, project)
	_, err := db.Exec(`UPDATE users SET subject='same-subject',provider='oauth' WHERE id='u1';
		UPDATE users SET subject='same-subject',provider='local' WHERE id='u2'`)
	must(t, err)
	rt, key := userParityRuntime(t, db)
	lookup := func(provider string) []*userread.UserView {
		input := &userread.UserInput{}
		input.SetSubject("same-subject")
		input.SetProvider(provider)
		value, err := rt.InvokeComponent(context.Background(), dexec.ComponentRequest{
			Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "GET", Path: "/v1/api/agently/user"}},
			Input:  input,
		})
		must(t, err)
		return value.(*userread.UserOutput).Data
	}
	if rows := lookup("oauth"); len(rows) != 1 || rows[0].Id != "u1" {
		t.Fatalf("oauth subject lookup = %+v", rows)
	}
	if rows := lookup("local"); len(rows) != 1 || rows[0].Id != "u2" {
		t.Fatalf("local subject lookup = %+v", rows)
	}
	if rows := lookup("missing"); len(rows) != 0 {
		t.Fatalf("unknown provider subject lookup = %+v", rows)
	}
}

func userParityFixture(t *testing.T, project string) (*sql.DB, string) {
	t.Helper()
	db, path := goalFixture(t, project)
	_, err := db.Exec(`INSERT INTO users(id,username,display_name,email,provider,hash_ip,timezone,settings,disabled,created_at,updated_at)
 VALUES('u1','first','seed name','seed@example.test','local','seed-hash','UTC','seed-settings',1,'2026-01-01 00:00:00','2026-01-01 00:00:00'),
 ('u2','second',NULL,NULL,'local',NULL,'UTC',NULL,0,'2026-01-01 00:00:00',NULL);
 CREATE TRIGGER reject_user BEFORE INSERT ON users WHEN NEW.id='u-reject' BEGIN SELECT RAISE(ABORT,'fixture user rejection'); END;`)
	must(t, err)
	return db, path
}

func userParityRuntime(t *testing.T, db *sql.DB) (*druntime.Runtime, spec.Key) {
	t.Helper()
	resources := resource.New()
	must(t, resources.Register(userread.ReaderDatlyResourceNamespace, userread.ReaderDatlyResources))
	must(t, resources.Register(userwrite.WriterDatlyResourceNamespace, userwrite.WriterDatlyResources))
	ra := payloadArtifact(t, resources, reflect.TypeFor[userread.ReaderComponent](), reflect.TypeFor[userread.UserInput](), reflect.TypeFor[userread.UserOutput]())
	wa := payloadArtifact(t, resources, reflect.TypeFor[userwrite.WriterComponent](), reflect.TypeFor[userwrite.Input](), reflect.TypeFor[userwrite.Output]())
	reader, err := ra.ReaderCompilation().NewExecution(bootstrap.ReaderRuntimeConfig{SQL: &dsql.SQLComponent{DB: db}})
	must(t, err)
	views, err := viewprovider.New(viewprovider.Config{Dependencies: wa.ViewDependencies, Input: wa.Input, SQL: &dsql.SQLComponent{DB: db}})
	must(t, err)
	handler, err := writer.New(wa.Component, reflect.TypeFor[userwrite.Input](), reflect.TypeFor[userwrite.Output](), "patch")
	must(t, err)
	rt, err := druntime.NewRuntime([]*registry.RegisteredComponent{
		{Component: ra.Component, Input: ra.Input, Output: ra.Output, OutputType: reflect.TypeFor[userread.UserOutput](), Reader: reader},
		{Component: wa.Component, Input: wa.Input, Output: wa.Output, OutputType: reflect.TypeFor[userwrite.Output](), Handler: handler, Providers: []locator.Provider{views}, DataSource: dml.Source{DB: db}},
	}, druntime.WithResources(resources))
	must(t, err)
	return rt, ra.Component.Key
}
