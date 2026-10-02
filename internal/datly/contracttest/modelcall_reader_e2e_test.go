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
	"sort"
	"strings"
	"testing"
	"time"

	read "github.com/viant/agently-core/internal/datly/modelcall/read"
	"github.com/viant/bindly/locator"
	requestprovider "github.com/viant/bindly/provider/request"
	"github.com/viant/bindly/resource"
	"github.com/viant/datly/bootstrap"
	dexec "github.com/viant/datly/exec"
	druntime "github.com/viant/datly/runtime"
	provider "github.com/viant/datly/runtime/handler/provider"
	"github.com/viant/datly/runtime/registry"
	"github.com/viant/datly/spec"
	dsql "github.com/viant/datly/sql"
)

func TestModelCallReaderLegacyTranscriptParity(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	legacy := legacyProbeBinary(t, project)
	type useCase struct {
		desc, input string
		expect      []string
	}
	for _, tc := range []useCase{
		{"assistant-only transcript scope preserves all physical fields", "c1", []string{"existing", "new"}},
		{"second conversation has isolated model calls", "c2", []string{"other"}},
		{"public conversation model call", "c3", []string{"public"}},
		{"missing conversation", "absent", []string{}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			_, oldPath := modelCallReaderFixture(t, project)
			db, _ := modelCallReaderFixture(t, project)
			payload, err := json.Marshal(map[string]any{"Component": "modelCallTranscript", "DBPath": oldPath, "Filters": map[string]any{"id": tc.input}})
			must(t, err)
			process := exec.Command(legacy)
			process.Stdin = bytes.NewReader(payload)
			raw, err := process.Output()
			must(t, err)
			var before probeResult
			must(t, json.Unmarshal(raw, &before))
			if before.Failed {
				t.Fatalf("legacy: %s", before.Error)
			}
			rt, key := modelCallReaderRuntime(t, db, "", "transcript", true, true)
			input := &read.ModelCallsInput{ConversationId: tc.input, Has: &read.ModelCallsInputHas{ConversationId: true}}
			out, err := rt.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "GET", Path: "/v1/internal/agently/model-call"}}, Input: input})
			must(t, err)
			raw, err = json.Marshal(out.(*read.ModelCallsOutput).Data)
			must(t, err)
			var rows []json.RawMessage
			must(t, json.Unmarshal(raw, &rows))
			fields := modelCallPhysicalFields(t, db)
			oldRows, newRows := modelCallReaderPhysicalRows(t, before.Rows, fields), modelCallReaderPhysicalRows(t, rows, fields)
			if !reflect.DeepEqual(oldRows, newRows) {
				t.Fatalf("scalar transcript legacy=%s native=%s", pretty(oldRows), pretty(newRows))
			}
			ids := []string{}
			for _, row := range newRows {
				ids = append(ids, row["messageid"].(string))
			}
			if !reflect.DeepEqual(ids, tc.expect) {
				t.Fatalf("ids=%v expected=%v", ids, tc.expect)
			}
		})
	}
}

func TestModelCallReaderScopeAndPredicates(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type input struct {
		subject, mode      string
		internal, provided bool
		request            *read.ModelCallsInput
	}
	type expect struct {
		failed bool
		ids    []string
	}
	type useCase struct {
		desc   string
		input  input
		expect expect
	}
	request := func() *read.ModelCallsInput { return &read.ModelCallsInput{Has: &read.ModelCallsInputHas{}} }
	for _, tc := range []useCase{
		{"anonymous sees only public facts", input{"", "rows", false, true, request()}, expect{ids: []string{"public"}}},
		{"owner sees own and public facts across roles", input{"u1", "rows", false, true, request()}, expect{ids: []string{"existing", "new", "public", "user-call"}}},
		{"other owner stays isolated", input{"u2", "rows", false, true, request()}, expect{ids: []string{"other", "public"}}},
		{"trusted table read includes all roles", input{"", "rows", true, true, request()}, expect{ids: []string{"existing", "new", "other", "public", "user-call"}}},
		{"ID cannot bypass private visibility", input{"u2", "rows", false, true, &read.ModelCallsInput{MessageId: "existing", Has: &read.ModelCallsInputHas{MessageId: true}}}, expect{ids: []string{}}},
		{"supplied empty ID remains an active predicate", input{"", "rows", true, true, &read.ModelCallsInput{MessageId: "", Has: &read.ModelCallsInputHas{MessageId: true}}}, expect{ids: []string{}}},
		{"status IN predicate", input{"", "rows", true, true, &read.ModelCallsInput{Statuses: []string{"thinking"}, Has: &read.ModelCallsInputHas{Statuses: true}}}, expect{ids: []string{"existing"}}},
		{"provider predicate", input{"", "rows", true, true, &read.ModelCallsInput{Provider: "openai", Has: &read.ModelCallsInputHas{Provider: true}}}, expect{ids: []string{"new"}}},
		{"model predicate", input{"", "rows", true, true, &read.ModelCallsInput{Model: "z", Has: &read.ModelCallsInputHas{Model: true}}}, expect{ids: []string{"public"}}},
		{"run predicate", input{"", "rows", true, true, &read.ModelCallsInput{RunId: "run-new", Has: &read.ModelCallsInputHas{RunId: true}}}, expect{ids: []string{"new"}}},
		{"turn predicate", input{"", "rows", true, true, &read.ModelCallsInput{TurnId: "t2", Has: &read.ModelCallsInputHas{TurnId: true}}}, expect{ids: []string{"other"}}},
		{"native pagination uses stable fact ordering", input{"", "rows", true, true, &read.ModelCallsInput{Limit: 1, Offset: 1, Has: &read.ModelCallsInputHas{Limit: true, Offset: true}}}, expect{ids: []string{"user-call"}}},

		{"time lower bound", input{"", "rows", true, true, &read.ModelCallsInput{StartedAfter: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC), Has: &read.ModelCallsInputHas{StartedAfter: true}}}, expect{ids: []string{"new", "public", "user-call"}}},
		{"time upper bound", input{"", "rows", true, true, &read.ModelCallsInput{StartedBefore: time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC), Has: &read.ModelCallsInputHas{StartedBefore: true}}}, expect{ids: []string{"existing", "new"}}},
		{"native projection does not fetch unselected metadata", input{"", "rows", true, true, &read.ModelCallsInput{Fields: []string{"message_id"}, Has: &read.ModelCallsInputHas{Fields: true}}}, expect{ids: []string{"existing", "new", "other", "public", "user-call"}}},
		{"missing host authorization fails closed", input{"", "rows", false, false, request()}, expect{failed: true}},
		{"unsupported host mode fails closed", input{"", "unknown", true, true, request()}, expect{failed: true}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			db, _ := modelCallReaderFixture(t, project)
			rt, key := modelCallReaderRuntime(t, db, tc.input.subject, tc.input.mode, tc.input.internal, tc.input.provided)
			value, err := rt.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "GET", Path: "/v1/internal/agently/model-call"}}, Input: tc.input.request})
			if (err != nil) != tc.expect.failed {
				t.Fatalf("failure=%v expected=%v", err, tc.expect.failed)
			}
			if err != nil {
				return
			}
			ids := []string{}

			for _, row := range value.(*read.ModelCallsOutput).Data {
				ids = append(ids, row.MessageId)
				if tc.input.request.Has != nil && tc.input.request.Has.Fields && (row.Provider != "" || row.Model != "" || row.Status != "" || row.StartedAt != nil || row.Cost != nil) {
					t.Fatal("projection fetched unselected metadata")
				}
			}

			sort.Strings(ids)
			if !reflect.DeepEqual(ids, tc.expect.ids) {
				t.Fatalf("ids=%v expected=%v", ids, tc.expect.ids)
			}
		})
	}
}
func modelCallReaderFixture(t *testing.T, project string) (*sql.DB, string) {
	db, path := modelCallFixture(t, project)
	_, err := db.Exec(`UPDATE model_call SET started_at='2026-01-01 00:00:00 +0000 UTC' WHERE message_id='existing'; UPDATE conversation SET created_by_user_id='u1' WHERE id='c1'; UPDATE conversation SET created_by_user_id='u2' WHERE id='c2'; UPDATE conversation SET visibility='public' WHERE id='c3';
 INSERT INTO turn(id,conversation_id,status) VALUES ('t2','c2','succeeded'),('t3','c3','succeeded');
 INSERT INTO message(id,conversation_id,turn_id,role) VALUES ('other','c2','t2','assistant'),('public','c3','t3','assistant'),('user-call','c1','t1','user');
 INSERT INTO model_call(message_id,turn_id,provider,model,model_kind,status,started_at,run_id) VALUES
 ('new','t1','openai','gpt','chat','completed','2026-01-02 00:00:00','run-new'),
 ('other','t2','other','m','chat','completed',NULL,NULL),
 ('public','t3','p','z','chat','pending','2026-01-03 00:00:00',NULL),
 ('user-call','t1','p','m','chat','completed','2026-01-03 00:00:00',NULL);`)
	must(t, err)
	return db, path
}
func modelCallReaderRuntime(t *testing.T, db *sql.DB, subject, mode string, internal, provided bool) (*druntime.Runtime, spec.Key) {
	resources := resource.New()
	must(t, resources.Register(read.ReaderDatlyResourceNamespace, read.ReaderDatlyResources))
	artifact := payloadArtifact(t, resources, reflect.TypeFor[read.ReaderComponent](), reflect.TypeFor[read.ModelCallsInput](), reflect.TypeFor[read.ModelCallsOutput]())
	execution, err := artifact.ReaderCompilation().NewExecution(bootstrap.ReaderRuntimeConfig{SQL: &dsql.SQLComponent{DB: db}})
	must(t, err)
	providers := []locator.Provider{}
	if provided {
		providers = append(providers, ordinaryAccess("modelcallaccess", func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
			if name == "mode" {
				return mode, true, nil
			}
			return internal, true, nil
		}), provider.Named("visibility", func(context.Context, reflect.Type, string) (any, bool, error) { return &subject, true, nil }))
	}
	rt, err := druntime.NewRuntime([]*registry.RegisteredComponent{{Component: artifact.Component, Input: artifact.Input, Output: artifact.Output, OutputType: reflect.TypeFor[read.ModelCallsOutput](), Reader: execution, Providers: providers}}, druntime.WithResources(resources))
	must(t, err)
	return rt, artifact.Component.Key
}
func modelCallPhysicalFields(t *testing.T, db *sql.DB) []string {
	rows, err := db.Query("SELECT * FROM model_call LIMIT 0")
	must(t, err)
	defer rows.Close()
	names, err := rows.Columns()
	must(t, err)
	for i, name := range names {
		names[i] = strings.ReplaceAll(name, "_", "")
	}
	return names
}
func modelCallReaderPhysicalRows(t *testing.T, rows []json.RawMessage, fields []string) []map[string]any {
	result := normalizeRowsInOrder(t, rows)
	for i, row := range result {
		physical := map[string]any{}
		for _, name := range fields {
			physical[name] = row[name]
		}
		result[i] = physical
	}
	sort.Slice(result, func(i, j int) bool { return result[i]["messageid"].(string) < result[j]["messageid"].(string) })
	return result
}

func TestModelCallReaderHTTPHostScope(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type input struct {
		subject, mode, url string
		internal           bool
	}
	type useCase struct {
		desc   string
		input  input
		expect []string
	}
	for _, tc := range []useCase{
		{"query cannot grant internal access or replace subject", input{"", "rows", "/v1/internal/agently/model-call?internal=true&visibilitySubject=u1&subject=u1", false}, []string{"public"}},
		{"query cannot replace trusted transcript mode", input{"", "transcript", "/v1/internal/agently/model-call?conversationId=c1&readMode=rows&mode=rows", true}, []string{"existing", "new"}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			db, _ := modelCallReaderFixture(t, project)
			rt, _ := modelCallReaderRuntime(t, db, tc.input.subject, tc.input.mode, tc.input.internal, true)
			request := httptest.NewRequest("GET", tc.input.url, nil)
			scope, err := requestprovider.New(request)
			must(t, err)
			defer scope.Close()
			value, err := rt.ExecuteRoute(context.Background(), "GET", "/v1/internal/agently/model-call", scope)
			must(t, err)
			ids := []string{}
			for _, row := range value.(*read.ModelCallsOutput).Data {
				ids = append(ids, row.MessageId)
			}
			sort.Strings(ids)
			if !reflect.DeepEqual(ids, tc.expect) {
				t.Fatalf("ids=%v expected=%v", ids, tc.expect)
			}
		})
	}
}
