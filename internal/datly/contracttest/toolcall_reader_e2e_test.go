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
	"testing"
	"time"

	read "github.com/viant/agently-core/internal/datly/toolcall/read"
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

func TestToolCallReaderLegacyParity(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	legacy := legacyProbeBinary(t, project)
	type input struct {
		mode    string
		filters map[string]any
	}
	type useCase struct {
		desc   string
		input  input
		expect []string
	}
	for _, tc := range []useCase{
		{"optional by-op conversation scope", input{"byOp", map[string]any{"opId": "shared", "conversationId": "c1"}}, []string{"existing", "inconsistent", "new"}},
		{"optional by-op scope may be absent for trusted call", input{"byOp", map[string]any{"opId": "shared"}}, []string{"existing", "inconsistent", "new", "other"}},
		{"optional by-op empty conversation preserves legacy omission", input{"byOp", map[string]any{"opId": "shared", "conversationId": ""}}, []string{"existing", "inconsistent", "new", "other"}},
		{"by-op missing operation", input{"byOp", map[string]any{"opId": "missing", "conversationId": "c1"}}, []string{}},
		{"scoped by-op preserves old contract", input{"scopedByOp", map[string]any{"opId": "shared", "conversationId": "c1"}}, []string{"existing", "inconsistent", "new"}},
		{"scoped by-op absent conversation matches empty", input{"scopedByOp", map[string]any{"opId": "shared"}}, []string{}},
		{"scoped by-op has isolated conversation", input{"scopedByOp", map[string]any{"opId": "shared", "conversationId": "c2"}}, []string{"other"}},
		{"by-turn enforces relationship consistency and ordered attempts", input{"byTurn", map[string]any{"conversationId": "c1", "turnId": "t1"}}, []string{"independent", "new", "existing"}},
		{"by-turn second conversation", input{"byTurn", map[string]any{"conversationId": "c2", "turnId": "t2"}}, []string{"other"}},
		{"by-turn wrong conversation is empty", input{"byTurn", map[string]any{"conversationId": "c2", "turnId": "t1"}}, []string{}},
		{"by-turn missing scope is empty", input{"byTurn", nil}, []string{}},
		{"by-turn missing turn is empty", input{"byTurn", map[string]any{"conversationId": "c1"}}, []string{}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			_, oldPath := toolCallReaderFixture(t, project)
			db, _ := toolCallReaderFixture(t, project)
			filters := map[string]any{"mode": tc.input.mode}
			for k, v := range tc.input.filters {
				filters[k] = v
			}
			payload, err := json.Marshal(map[string]any{"Component": "toolCallReader", "DBPath": oldPath, "Filters": filters})
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
			rt, key := toolCallReaderRuntime(t, db, "", tc.input.mode, true, true)
			input := &read.ToolCallsInput{Has: &read.ToolCallsInputHas{}}
			for _, field := range []struct {
				name  string
				value *string
				has   *bool
			}{{"conversationId", &input.ConversationId, &input.Has.ConversationId}, {"turnId", &input.TurnId, &input.Has.TurnId}, {"opId", &input.OpId, &input.Has.OpId}} {
				if value, ok := tc.input.filters[field.name]; ok {
					raw, err := json.Marshal(value)
					must(t, err)
					must(t, json.Unmarshal(raw, field.value))
					*field.has = true
				}
			}
			value, err := rt.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "GET", Path: "/v1/internal/agently/tool-call"}}, Input: input})
			must(t, err)
			out := value.(*read.ToolCallsOutput)
			raw, err = json.Marshal(out.Data)
			must(t, err)
			var rows []json.RawMessage
			must(t, json.Unmarshal(raw, &rows))
			fields := []string{"messageid", "turnid", "opid", "traceid", "responsepayloadid"}
			if tc.input.mode == "byTurn" {
				fields = []string{"messageid", "turnid", "opid", "attempt"}
			}
			oldRows, newRows := toolCallSelectedRows(t, before.Rows, fields), toolCallSelectedRows(t, rows, fields)
			if tc.input.mode != "byTurn" {
				sort.Slice(oldRows, func(i, j int) bool { return oldRows[i]["messageid"].(string) < oldRows[j]["messageid"].(string) })
				sort.Slice(newRows, func(i, j int) bool { return newRows[i]["messageid"].(string) < newRows[j]["messageid"].(string) })
			}
			if !reflect.DeepEqual(oldRows, newRows) {
				t.Fatalf("legacy=%s native=%s", pretty(oldRows), pretty(newRows))
			}
			ids := []string{}
			for _, row := range newRows {
				ids = append(ids, row["messageid"].(string))
			}
			if !reflect.DeepEqual(ids, tc.expect) {
				t.Fatalf("ids=%v expected=%v", ids, tc.expect)
			}
			for _, row := range out.Data {
				if row.ToolName != "" || row.ToolKind != "" || row.Status != "" || row.ErrorMessage != nil || row.StartedAt != nil {
					t.Fatal("lookup projection fetched unrelated metadata")
				}
			}
		})
	}
}

func TestToolCallReaderScopeAndPredicates(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type input struct {
		subject, mode      string
		internal, provided bool
		request            *read.ToolCallsInput
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
	request := func() *read.ToolCallsInput { return &read.ToolCallsInput{Has: &read.ToolCallsInputHas{}} }
	for _, tc := range []useCase{
		{"anonymous sees public fact", input{"", "rows", false, true, request()}, expect{ids: []string{"public"}}},
		{"owner sees own and public facts", input{"u1", "rows", false, true, request()}, expect{ids: []string{"existing", "inconsistent", "independent", "new", "public"}}},
		{"other owner is isolated", input{"u2", "rows", false, true, request()}, expect{ids: []string{"other", "public"}}},
		{"trusted full-table read", input{"", "rows", true, true, request()}, expect{ids: []string{"existing", "inconsistent", "independent", "new", "other", "public"}}},
		{"identity filter cannot bypass ownership", input{"u2", "rows", false, true, &read.ToolCallsInput{MessageId: "existing", Has: &read.ToolCallsInputHas{MessageId: true}}}, expect{ids: []string{}}},
		{"provided zero attempt remains active", input{"", "rows", true, true, &read.ToolCallsInput{Attempt: 0, Has: &read.ToolCallsInputHas{Attempt: true}}}, expect{ids: []string{"public"}}},
		{"provided empty identity remains active", input{"", "rows", true, true, &read.ToolCallsInput{MessageId: "", Has: &read.ToolCallsInputHas{MessageId: true}}}, expect{ids: []string{}}},
		{"tool name filter", input{"", "rows", true, true, &read.ToolCallsInput{ToolName: "different", Has: &read.ToolCallsInputHas{ToolName: true}}}, expect{ids: []string{"independent"}}},
		{"tool kind filter", input{"", "rows", true, true, &read.ToolCallsInput{ToolKind: "builtin", Has: &read.ToolCallsInputHas{ToolKind: true}}}, expect{ids: []string{"public"}}},
		{"run filter", input{"", "rows", true, true, &read.ToolCallsInput{RunId: "run-new", Has: &read.ToolCallsInputHas{RunId: true}}}, expect{ids: []string{"new"}}},
		{"status IN filter", input{"", "rows", true, true, &read.ToolCallsInput{Statuses: []string{"failed"}, Has: &read.ToolCallsInputHas{Statuses: true}}}, expect{ids: []string{"other"}}},

		{"time lower filter", input{"", "rows", true, true, &read.ToolCallsInput{StartedAfter: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC), Has: &read.ToolCallsInputHas{StartedAfter: true}}}, expect{ids: []string{"independent", "new", "public"}}},
		{"time upper filter", input{"", "rows", true, true, &read.ToolCallsInput{StartedBefore: time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC), Has: &read.ToolCallsInputHas{StartedBefore: true}}}, expect{ids: []string{"existing", "independent", "new"}}},
		{"native pagination uses stable ordering", input{"", "rows", true, true, &read.ToolCallsInput{Limit: 1, Offset: 1, Has: &read.ToolCallsInputHas{Limit: true, Offset: true}}}, expect{ids: []string{"independent"}}},
		{"native projected fields avoid metadata overfetch", input{"", "rows", true, true, &read.ToolCallsInput{Fields: []string{"message_id"}, Has: &read.ToolCallsInputHas{Fields: true}}}, expect{ids: []string{"existing", "inconsistent", "independent", "new", "other", "public"}}},
		{"missing host scope fails", input{"", "rows", false, false, request()}, expect{failed: true}},
		{"unsupported host mode fails", input{"", "invalid", true, true, request()}, expect{failed: true}},
		{"missing by-op identity fails", input{"", "byOp", true, true, request()}, expect{failed: true}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			db, _ := toolCallReaderFixture(t, project)
			rt, key := toolCallReaderRuntime(t, db, tc.input.subject, tc.input.mode, tc.input.internal, tc.input.provided)
			value, err := rt.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "GET", Path: "/v1/internal/agently/tool-call"}}, Input: tc.input.request})
			if (err != nil) != tc.expect.failed {
				t.Fatalf("failure=%v expected=%v", err, tc.expect.failed)
			}
			if err != nil {
				return
			}
			ids := []string{}
			for _, row := range value.(*read.ToolCallsOutput).Data {
				ids = append(ids, row.MessageId)
				if tc.input.request.Has != nil && tc.input.request.Has.Fields && (row.OpId != "" || row.ToolName != "" || row.Status != "" || row.StartedAt != nil) {
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
func toolCallReaderFixture(t *testing.T, project string) (*sql.DB, string) {
	db, path := toolCallFixture(t, project)
	_, err := db.Exec(`UPDATE conversation SET created_by_user_id='u1' WHERE id='c1';UPDATE conversation SET created_by_user_id='u2' WHERE id='c2';UPDATE conversation SET visibility='public' WHERE id='c3';
 UPDATE tool_call SET op_id='shared',attempt=1,started_at='2026-01-01 00:00:00 +0000 UTC' WHERE message_id='existing';
 INSERT INTO turn(id,conversation_id,status) VALUES ('t2','c2','running'),('t3','c3','running');
 UPDATE message SET type='tool_op' WHERE id IN ('existing','new');
 INSERT INTO message(id,conversation_id,turn_id,role,type) VALUES ('independent','c1','t1','tool','text'),('other','c2','t2','tool','text'),('public','c3','t3','tool','text'),('inconsistent','c1','t2','tool','text');
 INSERT INTO tool_call(message_id,turn_id,op_id,attempt,tool_name,tool_kind,status,run_id,started_at) VALUES
 ('new','t1','shared',2,'query','mcp','completed','run-new','2026-01-02 00:00:00'),
 ('independent','t1','a',1,'different','mcp','pending',NULL,'2026-01-02 00:00:00'),
 ('other','t2','shared',1,'query','mcp','failed',NULL,NULL),
 ('public','t3','public-op',0,'query','builtin','completed',NULL,'2026-01-03 00:00:00'),
 ('inconsistent','t1','shared',3,'query','mcp','running',NULL,NULL);`)
	must(t, err)
	return db, path
}
func toolCallReaderRuntime(t *testing.T, db *sql.DB, subject, mode string, internal, provided bool) (*druntime.Runtime, spec.Key) {
	resources := resource.New()
	must(t, resources.Register(read.ReaderDatlyResourceNamespace, read.ReaderDatlyResources))
	artifact := payloadArtifact(t, resources, reflect.TypeFor[read.ReaderComponent](), reflect.TypeFor[read.ToolCallsInput](), reflect.TypeFor[read.ToolCallsOutput]())
	execution, err := artifact.ReaderCompilation().NewExecution(bootstrap.ReaderRuntimeConfig{SQL: &dsql.SQLComponent{DB: db}})
	must(t, err)
	providers := []locator.Provider{}
	if provided {
		providers = append(providers, ordinaryAccess("toolcallaccess", func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
			if name == "mode" {
				return mode, true, nil
			}
			return internal, true, nil
		}), provider.Named("visibility", func(context.Context, reflect.Type, string) (any, bool, error) { return &subject, true, nil }))
	}
	rt, err := druntime.NewRuntime([]*registry.RegisteredComponent{{Component: artifact.Component, Input: artifact.Input, Output: artifact.Output, OutputType: reflect.TypeFor[read.ToolCallsOutput](), Reader: execution, Providers: providers}}, druntime.WithResources(resources))
	must(t, err)
	return rt, artifact.Component.Key
}
func toolCallSelectedRows(t *testing.T, rows []json.RawMessage, fields []string) []map[string]any {
	result := normalizeRowsInOrder(t, rows)
	for i, row := range result {
		selected := map[string]any{}
		for _, field := range fields {
			selected[field] = row[field]
		}
		result[i] = selected
	}
	return result
}

func TestToolCallReaderHTTPHostScope(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type input struct {
		mode, url string
		internal  bool
	}
	type useCase struct {
		desc   string
		input  input
		expect []string
	}
	for _, tc := range []useCase{
		{"query cannot grant internal access or replace subject", input{"rows", "/v1/internal/agently/tool-call?internal=true&visibilitySubject=u1&subject=u1", false}, []string{"public"}},
		{"query cannot replace host by-turn consistency mode", input{"byTurn", "/v1/internal/agently/tool-call?conversationId=c1&turnId=t1&readMode=rows&mode=rows", true}, []string{"independent", "new", "existing"}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			db, _ := toolCallReaderFixture(t, project)
			rt, _ := toolCallReaderRuntime(t, db, "", tc.input.mode, tc.input.internal, true)
			request := httptest.NewRequest("GET", tc.input.url, nil)
			scope, err := requestprovider.New(request)
			must(t, err)
			defer scope.Close()
			value, err := rt.ExecuteRoute(context.Background(), "GET", "/v1/internal/agently/tool-call", scope)
			must(t, err)
			ids := []string{}
			for _, row := range value.(*read.ToolCallsOutput).Data {
				ids = append(ids, row.MessageId)
			}
			if !reflect.DeepEqual(ids, tc.expect) {
				t.Fatalf("ids=%v expected=%v", ids, tc.expect)
			}
		})
	}
}

func TestToolCallReaderHistoricalTimestamp(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	db, _ := toolCallReaderFixture(t, project)
	rt, key := toolCallReaderRuntime(t, db, "", "rows", true, true)
	input := &read.ToolCallsInput{MessageId: "existing", Has: &read.ToolCallsInputHas{MessageId: true}}
	value, err := rt.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "GET", Path: "/v1/internal/agently/tool-call"}}, Input: input})
	must(t, err)
	rows := value.(*read.ToolCallsOutput).Data
	expected := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if len(rows) != 1 || rows[0].StartedAt == nil || !rows[0].StartedAt.Equal(expected) {
		t.Fatal("historical timestamp did not survive native reader execution")
	}
}
