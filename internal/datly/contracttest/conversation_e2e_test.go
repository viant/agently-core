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
	"time"

	conversationread "github.com/viant/agently-core/internal/datly/conversation/read"
	conversationwrite "github.com/viant/agently-core/internal/datly/conversation/write"
	"github.com/viant/bindly/locator"
	requestprovider "github.com/viant/bindly/provider/request"
	"github.com/viant/bindly/resource"
	"github.com/viant/datly/bootstrap"
	dexec "github.com/viant/datly/exec"
	druntime "github.com/viant/datly/runtime"
	provider "github.com/viant/datly/runtime/handler/provider"
	writer "github.com/viant/datly/runtime/handler/writer"
	"github.com/viant/datly/runtime/registry"
	"github.com/viant/datly/spec"
	dsql "github.com/viant/datly/sql"
	"github.com/viant/datly/sql/dml"
	viewprovider "github.com/viant/datly/sql/reader/provider"
)

func TestConversationLegacyV1Parity(t *testing.T) {
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
	for _, tc := range []useCase{
		{desc: "empty transcript preserves conversation", expect: expect{ids: []string{"c1"}}},
		{desc: "unknown conversation is empty", input: input{filters: map[string]any{"id": "absent"}}, expect: expect{ids: []string{}}},
		{desc: "new conversation applies creation and visibility defaults", input: input{body: `{"data":[{"id":"c-new","title":"new"}]}`, filters: map[string]any{"id": "c-new"}}, expect: expect{ids: []string{"c-new"}, fields: map[string]map[string]any{"c-new": {"title": "new", "visibility": "private", "shareable": float64(0)}}}},
		{desc: "new creation timestamp replaces client timestamp", input: input{body: `{"data":[{"id":"c-new","createdAt":"2026-01-01T00:00:00Z"}]}`, filters: map[string]any{"id": "c-new"}}, expect: expect{ids: []string{"c-new"}, fields: map[string]map[string]any{"c-new": {"createdat": "<generated timestamp>"}}}},
		{desc: "status patch preserves metadata", input: input{body: `{"data":[{"id":"c1","status":"succeeded"}]}`}, expect: expect{fields: map[string]map[string]any{"c1": {"status": "succeeded", "metadata": "{\"workspace\":{\"windowId\":\"order_1\"}}"}}}},
		{desc: "explicit null metadata clears only metadata", input: input{body: `{"data":[{"id":"c1","metadata":null}]}`}, expect: expect{fields: map[string]map[string]any{"c1": {"metadata": nil, "summary": "original"}}}},
		{desc: "explicit zero and empty values are preserved", input: input{body: `{"data":[{"id":"c1","shareable":0,"title":""}]}`}, expect: expect{fields: map[string]map[string]any{"c1": {"shareable": float64(0), "title": ""}}}},
		{desc: "nil collection is no-op", input: input{body: `{"data":null}`}},
		{desc: "empty collection is no-op", input: input{body: `{"data":[]}`}},
		{desc: "missing identity is rejected", input: input{body: `{"data":[{"title":"invalid"}]}`}, expect: expect{failed: true}},
		{desc: "late insert failure rolls back earlier status patch", input: input{body: `{"data":[{"id":"c1","status":"completed"},{"id":"c-reject"}]}`}, expect: expect{failed: true, fields: map[string]map[string]any{"c1": {"status": "running"}}}},
		{desc: "transcript computes succeeded turn and conversation stage", input: input{filters: map[string]any{"id": "c-transcript"}}, expect: expect{ids: []string{"c-transcript"}, fields: map[string]map[string]any{"c-transcript": {"stage": "done", "status": "succeeded"}}}},
		{desc: "transcript inclusion false omits child turns", input: input{filters: map[string]any{"id": "c-transcript", "includeTranscript": false}}, expect: expect{ids: []string{"c-transcript"}}},
		{desc: "model calls and payloads are included explicitly", input: input{filters: map[string]any{"id": "c-transcript", "includeModelCall": true}}, expect: expect{ids: []string{"c-transcript"}}},
		{desc: "tool calls are included explicitly", input: input{filters: map[string]any{"id": "c-transcript", "includeToolCall": true}}, expect: expect{ids: []string{"c-transcript"}}},
		{desc: "model and tool graph inclusion together preserves scoped payloads", input: input{filters: map[string]any{"id": "c-transcript", "includeModelCall": true, "includeToolCall": true}}, expect: expect{ids: []string{"c-transcript"}}},
		{desc: "elicitation payload hydration retains pending status", input: input{filters: map[string]any{"id": "c-elicit"}}, expect: expect{ids: []string{"c-elicit"}, fields: map[string]map[string]any{"c-elicit": {"stage": "elicitation", "status": "queued"}}}},
		{desc: "since turn is inclusive", input: input{filters: map[string]any{"id": "c-transcript", "since": "t1"}}, expect: expect{ids: []string{"c-transcript"}}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			_, oldPath := conversationParityFixture(t, project)
			db, _ := conversationParityFixture(t, project)
			filters := tc.input.filters
			if filters == nil {
				filters = map[string]any{}
			}
			probe := struct {
				Component, DBPath, Body string
				Filters                 map[string]any
			}{"conversation", oldPath, tc.input.body, filters}
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
			rt, key := conversationParityRuntime(t, db)
			var afterOutput json.RawMessage
			var mutationError error
			if tc.input.body != "" {
				req := httptest.NewRequest("PATCH", "/v1/api/agently/conversation", strings.NewReader(tc.input.body))
				req.Header.Set("Content-Type", "application/json")
				scope, err := requestprovider.New(req)
				must(t, err)
				defer scope.Close()
				result, err := rt.ExecuteRoute(context.Background(), "PATCH", "/v1/api/agently/conversation", scope)
				mutationError = err
				if err == nil {
					afterOutput, err = json.Marshal(result.(*conversationwrite.Output).Data)
					must(t, err)
				}
			}
			if before.Failed != tc.expect.failed || (mutationError != nil) != tc.expect.failed {
				t.Fatalf("outcome legacy=%v (%s) v1=%v expected failure=%v", before.Failed, before.Error, mutationError, tc.expect.failed)
			}
			readerInput := &conversationread.ConversationInput{Id: "c1", IncludeTranscript: true, Has: &conversationread.ConversationInputHas{Id: true, IncludeTranscript: true, IncludeModelCal: true, IncludeToolCall: true}}
			for _, field := range []struct {
				name    string
				value   any
				present *bool
			}{
				{"id", &readerInput.Id, &readerInput.Has.Id}, {"since", &readerInput.Since, &readerInput.Has.Since}, {"includeTranscript", &readerInput.IncludeTranscript, &readerInput.Has.IncludeTranscript}, {"includeModelCall", &readerInput.IncludeModelCal, &readerInput.Has.IncludeModelCal}, {"includeToolCall", &readerInput.IncludeToolCall, &readerInput.Has.IncludeToolCall},
			} {
				if value, present := filters[field.name]; present {
					raw, err := json.Marshal(value)
					must(t, err)
					must(t, json.Unmarshal(raw, field.value))
					*field.present = true
				}
			}
			output, err := rt.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "GET", Path: "/v1/api/agently/conversation/{id}"}}, Input: readerInput})
			must(t, err)
			data, err := json.Marshal(output.(*conversationread.ConversationOutput).Data)
			must(t, err)
			var rows []json.RawMessage
			must(t, json.Unmarshal(data, &rows))
			oldRows, newRows := normalizeConversationRows(t, before.Rows), normalizeConversationRows(t, rows)
			if !reflect.DeepEqual(oldRows, newRows) {
				t.Fatalf("conversation parity\nlegacy=%s\nv1=%s", pretty(oldRows), pretty(newRows))
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
				expectedIDs = []string{"c1"}
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
				if !reflect.DeepEqual(normalizeConversationRows(t, old), normalizeConversationRows(t, new)) {
					t.Fatalf("write response parity\nlegacy=%s\nv1=%s", before.Output, afterOutput)
				}
			}
		})
	}
}

func conversationParityFixture(t *testing.T, project string) (*sql.DB, string) {
	t.Helper()
	db, path := goalFixture(t, project)
	_, err := db.Exec(`
 UPDATE conversation SET summary='original',metadata='{"workspace":{"windowId":"order_1"}}',status='running',shareable=1,created_at='2026-01-01 00:00:00',last_activity='2026-01-01 00:00:00' WHERE id='c1';
 INSERT INTO conversation(id,status,created_at) VALUES('c-transcript','running','2026-01-01 00:00:00');
 INSERT INTO turn(id,conversation_id,status,created_at,queue_seq) VALUES('t1','c-transcript','succeeded','2026-01-01 00:00:00',1);
 INSERT INTO message(id,conversation_id,turn_id,role,content,created_at,status) VALUES('m1','c-transcript','t1','assistant','done','2026-01-01 00:00:00','succeeded');
 INSERT INTO call_payload(id,kind,mime_type,size_bytes,storage,inline_body,compression) VALUES('p-model','request','application/json',2,'inline','{}','none'),('p-tool','response','application/json',2,'inline','{}','none'),('p-elicit','elicitation','application/json',18,'inline','{"message":"pick"}','none');
 INSERT INTO model_call(message_id,turn_id,provider,model,model_kind,status,prompt_tokens,completion_tokens,total_tokens,cost,latency_ms,completed_at,request_payload_id,trace_id) VALUES('m1','t1','test','model','chat','succeeded',10,5,15,0.1,20,'2026-01-01 00:00:00','p-model','trace1');
 INSERT INTO message(id,conversation_id,turn_id,role,type,parent_message_id,content,created_at,status,sequence) VALUES('m-tool','c-transcript','t1','tool','tool_op','m1','tool result','2026-01-01 00:00:00','succeeded',1);
 INSERT INTO tool_call(message_id,turn_id,op_id,tool_name,tool_kind,status,response_payload_id,completed_at,trace_id) VALUES('m-tool','t1','op1','test','local','succeeded','p-tool','2026-01-01 00:00:00','trace1');
 INSERT INTO conversation(id,created_at) VALUES('c-elicit','2026-01-01 00:00:00');
 INSERT INTO turn(id,conversation_id,status,created_at,queue_seq) VALUES('t-elicit','c-elicit','queued','2026-01-01 00:00:00',1);
 INSERT INTO message(id,conversation_id,turn_id,role,content,created_at,status,elicitation_id,elicitation_payload_id) VALUES('m-elicit','c-elicit','t-elicit','assistant','{"message":"pick"}','2026-01-01 00:00:00','pending','e1','p-elicit');
 CREATE TRIGGER reject_conversation BEFORE INSERT ON conversation WHEN NEW.id='c-reject' BEGIN SELECT RAISE(ABORT,'fixture conversation rejection'); END;`)
	must(t, err)
	return db, path
}

func conversationParityRuntime(t *testing.T, db *sql.DB, supplied ...*sql.Tx) (*druntime.Runtime, spec.Key) {
	t.Helper()
	resources := resource.New()
	must(t, resources.Register(conversationread.ReaderDatlyResourceNamespace, conversationread.ReaderDatlyResources))
	must(t, resources.Register(conversationwrite.WriterDatlyResourceNamespace, conversationwrite.WriterDatlyResources))
	ra := payloadArtifact(t, resources, reflect.TypeFor[conversationread.ReaderComponent](), reflect.TypeFor[conversationread.ConversationInput](), reflect.TypeFor[conversationread.ConversationOutput]())
	wa := payloadArtifact(t, resources, reflect.TypeFor[conversationwrite.WriterComponent](), reflect.TypeFor[conversationwrite.Input](), reflect.TypeFor[conversationwrite.Output]())
	reader, err := ra.ReaderCompilation().NewExecution(bootstrap.ReaderRuntimeConfig{SQL: &dsql.SQLComponent{DB: db}})
	must(t, err)
	views, err := viewprovider.New(viewprovider.Config{Dependencies: wa.ViewDependencies, Input: wa.Input, SQL: &dsql.SQLComponent{DB: db}})
	must(t, err)
	handler, err := writer.New(wa.Component, reflect.TypeFor[conversationwrite.Input](), reflect.TypeFor[conversationwrite.Output](), "patch")
	must(t, err)
	var tx *sql.Tx
	if len(supplied) > 0 {
		tx = supplied[0]
	}
	rt, err := druntime.NewRuntime([]*registry.RegisteredComponent{
		{Component: ra.Component, Input: ra.Input, Output: ra.Output, OutputType: reflect.TypeFor[conversationread.ConversationOutput](), Reader: reader, Providers: []locator.Provider{ordinaryAccess("conversationaccess", func(context.Context, reflect.Type, string) (any, bool, error) { return false, true, nil }), provider.Named("visibility", func(context.Context, reflect.Type, string) (any, bool, error) {
			subject := ""
			return &subject, true, nil
		})}},
		{Component: wa.Component, Input: wa.Input, Output: wa.Output, OutputType: reflect.TypeFor[conversationwrite.Output](), Handler: handler, Providers: []locator.Provider{views}, DataSource: dml.Source{DB: db, Tx: tx}},
	}, druntime.WithResources(resources))
	must(t, err)
	return rt, ra.Component.Key
}

func normalizeConversationRows(t *testing.T, rows []json.RawMessage) []map[string]any {
	t.Helper()
	var normalize func(any, string) any
	normalize = func(value any, key string) any {
		switch v := value.(type) {
		case map[string]any:
			result := map[string]any{}
			for k, child := range v {
				k = strings.ToLower(k)
				result[k] = normalize(child, k)
			}
			return result
		case []any:
			result := make([]any, len(v))
			for i, child := range v {
				result[i] = normalize(child, "")
			}
			return result
		case string:
			if key == "createdat" || key == "updatedat" || key == "lastactivity" {
				parsed, err := time.Parse(time.RFC3339Nano, v)
				if err == nil && !parsed.Equal(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)) {
					return "<generated timestamp>"
				}
			}
		}
		return value
	}
	result := []map[string]any{}
	for _, raw := range rows {
		var value map[string]any
		must(t, json.Unmarshal(raw, &value))
		result = append(result, normalize(value, "").(map[string]any))
	}
	return result
}

func TestConversationCallerTransactionOwnership(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "caller rollback", true: "caller commit"}[commit], func(t *testing.T) {
			db, _ := conversationParityFixture(t, project)
			db.SetMaxOpenConns(1)
			tx, err := db.BeginTx(context.Background(), nil)
			must(t, err)
			defer tx.Rollback()
			rt, _ := conversationParityRuntime(t, db, tx)
			request := httptest.NewRequest("PATCH", "/v1/api/agently/conversation", strings.NewReader(`{"data":[{"id":"c1","status":"completed"}]}`))
			request.Header.Set("Content-Type", "application/json")
			scope, err := requestprovider.New(request)
			must(t, err)
			defer scope.Close()
			_, err = rt.ExecuteRoute(context.Background(), "PATCH", "/v1/api/agently/conversation", scope)
			must(t, err)
			var status string
			must(t, tx.QueryRow("SELECT status FROM conversation WHERE id='c1'").Scan(&status))
			if status != "completed" {
				t.Fatalf("pending transaction status=%s", status)
			}
			if commit {
				must(t, tx.Commit())
			} else {
				must(t, tx.Rollback())
			}
			must(t, db.QueryRow("SELECT status FROM conversation WHERE id='c1'").Scan(&status))
			expected := "running"
			if commit {
				expected = "completed"
			}
			if status != expected {
				t.Fatalf("stored status=%s expected=%s", status, expected)
			}
		})
	}
}
