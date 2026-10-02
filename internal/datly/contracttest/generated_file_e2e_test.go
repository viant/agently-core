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

	fileread "github.com/viant/agently-core/internal/datly/generatedfile/read"
	filewrite "github.com/viant/agently-core/internal/datly/generatedfile/write"
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

func TestGeneratedFileLegacyV1Parity(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	legacy := legacyProbeBinary(t, project)
	type input struct {
		body    string
		filters map[string]any
	}
	type expect struct {
		failed                bool
		legacyStatusCollision bool
		ids                   []string
		fields                map[string]map[string]any
	}
	type useCase struct {
		desc   string
		input  input
		expect expect
	}
	insertion := `{"id":"f-new","conversationId":"c1","provider":"openai","mode":"generate","copyMode":"inline"}`
	for _, tc := range []useCase{
		{desc: "conversation scope excludes a foreign conversation", expect: expect{ids: []string{"f1", "f3"}}},
		{desc: "native status filter fixes legacy input output Status collision", input: input{filters: map[string]any{"conversationId": "c1", "provider": "other", "status": "failed"}}, expect: expect{ids: []string{"f3"}, legacyStatusCollision: true}},
		{desc: "turn and message predicates combine", input: input{filters: map[string]any{"turnId": "t1", "messageId": "m1"}}, expect: expect{ids: []string{"f1"}}},
		{desc: "since retains legacy text datetime boundary behavior", input: input{filters: map[string]any{"conversationId": "c1", "since": "2026-01-02T00:00:00Z"}}, expect: expect{ids: []string{}}},
		{desc: "missing ID returns an empty result", input: input{filters: map[string]any{"id": "absent"}}, expect: expect{ids: []string{}}},
		{desc: "insertion defaults status and timestamps", input: input{body: `{"data":[` + insertion + `]}`}, expect: expect{ids: []string{"f-new", "f1", "f3"}, fields: map[string]map[string]any{"f-new": {"status": "ready", "createdat": "<generated timestamp>", "updatedat": "<generated timestamp>"}}}},
		{desc: "explicit empty insertion status still receives ready default", input: input{body: `{"data":[{"id":"f-new","conversationId":"c1","provider":"openai","mode":"generate","copyMode":"inline","status":""}]}`}, expect: expect{ids: []string{"f-new", "f1", "f3"}, fields: map[string]map[string]any{"f-new": {"status": "ready"}}}},
		{desc: "provided creation and update timestamps remain authoritative", input: input{body: `{"data":[{"id":"f-new","conversationId":"c1","provider":"openai","mode":"generate","copyMode":"inline","status":"queued","createdAt":"2026-01-01T00:00:00Z","updatedAt":"2026-01-01T00:00:00Z"}]}`}, expect: expect{ids: []string{"f-new", "f1", "f3"}, fields: map[string]map[string]any{"f-new": {"status": "queued", "createdat": "2026-01-01T00:00:00Z", "updatedat": "2026-01-01T00:00:00Z"}}}},
		{desc: "sparse filename update preserves required omitted fields", input: input{body: `{"data":[{"id":"f1","filename":"changed.txt"}]}`}, expect: expect{fields: map[string]map[string]any{"f1": {"filename": "changed.txt", "provider": "openai", "status": "ready", "conversationid": "c1", "checksum": "seed-checksum", "createdat": "2026-01-01T00:00:00Z"}}}},
		{desc: "explicit zero and null differ from omission", input: input{body: `{"data":[{"id":"f1","sizeBytes":0,"checksum":null,"filename":"","expiresAt":null}]}`}, expect: expect{fields: map[string]map[string]any{"f1": {"sizebytes": float64(0), "checksum": nil, "filename": "", "expiresat": nil}}}},
		{desc: "mixed update and insert preserve response order", input: input{body: `{"data":[{"id":"f1","status":"failed","errorMessage":"test"},` + insertion + `]}`}, expect: expect{ids: []string{"f-new", "f1", "f3"}, fields: map[string]map[string]any{"f1": {"status": "failed", "errormessage": "test"}, "f-new": {"status": "ready"}}}},
		{desc: "new row requires provider", input: input{body: `{"data":[{"id":"f-new","conversationId":"c1","mode":"generate","copyMode":"inline"}]}`}, expect: expect{failed: true}},
		{desc: "existing required provider cannot become empty", input: input{body: `{"data":[{"id":"f1","provider":""}]}`}, expect: expect{failed: true}},
		{desc: "explicit null creation timestamp fails instead of defaulting", input: input{body: `{"data":[{"id":"f-new","conversationId":"c1","provider":"openai","mode":"generate","copyMode":"inline","createdAt":null}]}`}, expect: expect{failed: true}},
		{desc: "missing conversation reference fails", input: input{body: `{"data":[{"id":"f-new","conversationId":"absent","provider":"openai","mode":"generate","copyMode":"inline"}]}`}, expect: expect{failed: true}},
		{desc: "late database rejection rolls back earlier sparse update", input: input{body: `{"data":[{"id":"f1","filename":"changed.txt"},{"id":"f-reject","conversationId":"c1","provider":"openai","mode":"generate","copyMode":"inline"}]}`}, expect: expect{failed: true, fields: map[string]map[string]any{"f1": {"filename": "seed.txt"}}}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			_, oldPath := generatedFileFixture(t, project)
			db, _ := generatedFileFixture(t, project)
			filters := tc.input.filters
			if filters == nil {
				filters = map[string]any{"conversationId": "c1"}
			}
			probe := struct {
				Component, DBPath, Body string
				Filters                 map[string]any
			}{"generatedFile", oldPath, tc.input.body, filters}
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
			rt, key := generatedFileRuntime(t, db)
			var afterOutput json.RawMessage
			var mutationError error
			if tc.input.body != "" {
				req := httptest.NewRequest("PATCH", "/v1/api/agently/generated-file", strings.NewReader(tc.input.body))
				req.Header.Set("Content-Type", "application/json")
				scope, err := requestprovider.New(req)
				must(t, err)
				defer scope.Close()
				result, err := rt.ExecuteRoute(context.Background(), "PATCH", "/v1/api/agently/generated-file", scope)
				mutationError = err
				if err == nil {
					afterOutput, err = json.Marshal(result.(*filewrite.Output).Data)
					must(t, err)
				}
			}
			if before.Failed != tc.expect.failed || (mutationError != nil) != tc.expect.failed {
				t.Fatalf("outcome legacy=%v (%s) v1=%v expected failure=%v", before.Failed, before.Error, mutationError, tc.expect.failed)
			}
			readerInput := &fileread.Input{Has: &fileread.InputHas{}}
			for name, value := range filters {
				encoded, err := json.Marshal(value)
				must(t, err)
				switch name {
				case "conversationId":
					must(t, json.Unmarshal(encoded, &readerInput.ConversationID))
					readerInput.Has.ConversationID = true
				case "turnId":
					must(t, json.Unmarshal(encoded, &readerInput.TurnID))
					readerInput.Has.TurnID = true
				case "messageId":
					must(t, json.Unmarshal(encoded, &readerInput.MessageID))
					readerInput.Has.MessageID = true
				case "id":
					must(t, json.Unmarshal(encoded, &readerInput.ID))
					readerInput.Has.ID = true
				case "provider":
					must(t, json.Unmarshal(encoded, &readerInput.Provider))
					readerInput.Has.Provider = true
				case "status":
					must(t, json.Unmarshal(encoded, &readerInput.Status))
					readerInput.Has.Status = true
				case "since":
					must(t, json.Unmarshal(encoded, &readerInput.Since))
					readerInput.Has.Since = true
				}
			}
			output, err := rt.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "GET", Path: "/v2/api/agently/generated-file"}}, Input: readerInput})
			must(t, err)
			data, err := json.Marshal(output.(*fileread.Output).Data)
			must(t, err)
			var rows []json.RawMessage
			must(t, json.Unmarshal(data, &rows))
			oldRows, newRows := normalizeRows(t, before.Rows), normalizeRows(t, rows)
			if tc.expect.legacyStatusCollision {
				if len(oldRows) != 0 {
					t.Fatalf("legacy collision behavior changed: %s", pretty(oldRows))
				}
			} else if !reflect.DeepEqual(oldRows, newRows) {
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
				expectedIDs = []string{"f1", "f3"}
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

func generatedFileFixture(t *testing.T, project string) (*sql.DB, string) {
	t.Helper()
	db, path := goalFixture(t, project)
	_, err := db.Exec(`INSERT INTO generated_file(id,conversation_id,turn_id,message_id,provider,mode,copy_mode,status,filename,size_bytes,checksum,expires_at,created_at,updated_at)
 VALUES('f1','c1','t1','m1','openai','generate','inline','ready','seed.txt',42,'seed-checksum','2027-01-01 00:00:00','2026-01-01 00:00:00','2026-01-01 00:00:00'),
 ('f2','c2','t2','m2','openai','generate','inline','ready','foreign.txt',10,NULL,NULL,'2026-01-01 00:00:00','2026-01-01 00:00:00'),
 ('f3','c1',NULL,NULL,'other','generate','inline','failed',NULL,0,NULL,NULL,'2026-01-02 00:00:00','2026-01-01 00:00:00');
 CREATE TRIGGER reject_generated_file BEFORE INSERT ON generated_file WHEN NEW.id='f-reject' BEGIN SELECT RAISE(ABORT,'fixture generated file rejection'); END;`)
	must(t, err)
	return db, path
}

func generatedFileRuntime(t *testing.T, db *sql.DB) (*druntime.Runtime, spec.Key) {
	t.Helper()
	resources := resource.New()
	must(t, resources.Register(fileread.ReaderDatlyResourceNamespace, fileread.ReaderDatlyResources))
	must(t, resources.Register(filewrite.WriterDatlyResourceNamespace, filewrite.WriterDatlyResources))
	ra := payloadArtifact(t, resources, reflect.TypeFor[fileread.ReaderComponent](), reflect.TypeFor[fileread.Input](), reflect.TypeFor[fileread.Output]())
	wa := payloadArtifact(t, resources, reflect.TypeFor[filewrite.WriterComponent](), reflect.TypeFor[filewrite.Input](), reflect.TypeFor[filewrite.Output]())
	reader, err := ra.ReaderCompilation().NewExecution(bootstrap.ReaderRuntimeConfig{SQL: &dsql.SQLComponent{DB: db}})
	must(t, err)
	views, err := viewprovider.New(viewprovider.Config{Dependencies: wa.ViewDependencies, Input: wa.Input, SQL: &dsql.SQLComponent{DB: db}})
	must(t, err)
	handler, err := writer.New(wa.Component, reflect.TypeFor[filewrite.Input](), reflect.TypeFor[filewrite.Output](), "patch")
	must(t, err)
	rt, err := druntime.NewRuntime([]*registry.RegisteredComponent{
		{Component: ra.Component, Input: ra.Input, Output: ra.Output, OutputType: reflect.TypeFor[fileread.Output](), Reader: reader},
		{Component: wa.Component, Input: wa.Input, Output: wa.Output, OutputType: reflect.TypeFor[filewrite.Output](), Handler: handler, Providers: []locator.Provider{views}, DataSource: dml.Source{DB: db}},
	}, druntime.WithResources(resources))
	must(t, err)
	return rt, ra.Component.Key
}
