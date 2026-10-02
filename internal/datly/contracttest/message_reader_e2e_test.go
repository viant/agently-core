package tests

import (
	"bytes"
	"compress/gzip"
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

	read "github.com/viant/agently-core/internal/datly/message/read"
	"github.com/viant/agently-core/internal/datly/queryselectors"
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
	"github.com/viant/xdatly/state"
)

func TestMessageReaderLegacyParity(t *testing.T) {
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
		{"conversation scope", input{"rows", map[string]any{"conversationId": "c1"}}, []string{"a", "b", "c", "d", "e"}},
		{"turn scope", input{"rows", map[string]any{"turnId": "t1"}}, []string{"a", "b", "c", "d", "e"}},
		{"role IN", input{"rows", map[string]any{"roles": []string{"user"}}}, []string{"a"}},
		{"type IN", input{"rows", map[string]any{"types": []string{"elicitation_response"}}}, []string{"d"}},
		{"provided zero interim", input{"rows", map[string]any{"interim": 0}}, []string{"a", "c", "d", "e", "other"}},
		{"phase filter", input{"rows", map[string]any{"phase": "answer"}}, []string{"b", "c"}},
		{"provided zero iteration", input{"rows", map[string]any{"iteration": 0}}, []string{"a"}},
		{"time lower bound", input{"rows", map[string]any{"conversationId": "c1", "createdSince": "2026-01-03T00:00:00Z"}}, []string{"d", "e"}},
		{"time upper bound", input{"rows", map[string]any{"createdBefore": "2026-01-02T00:00:00Z"}}, []string{"a", "b"}},
		{"cursor before", input{"rows", map[string]any{"cursorBefore": "c"}}, []string{"a", "b"}},
		{"cursor after", input{"rows", map[string]any{"cursorAfter": "c"}}, []string{"d", "e", "other"}},
		{"missing cursor returns empty", input{"rows", map[string]any{"cursorBefore": "absent"}}, []string{}},
		{"task predicate", input{"rows", map[string]any{"turnTask": true}}, []string{"a"}},
		{"assistant final excludes router", input{"rows", map[string]any{"assistantFinal": true}}, []string{"c"}},
		{"assistant progress narration", input{"rows", map[string]any{"assistantStatus": true}}, []string{"b"}},
		{"false boolean trigger preserves legacy presence evaluation", input{"rows", map[string]any{"assistantStatus": false}}, []string{"b"}},
		{"scalar id lookup hydrates payload", input{"byId", map[string]any{"id": "b"}}, []string{"b"}},
		{"latest elicitation excludes response", input{"elicitation", map[string]any{"conversationId": "c1", "elicitationId": "el"}}, []string{"c"}},
		{"linked elicitation", input{"linkedElicitation", map[string]any{"linkedConversationId": "child", "elicitationId": "el"}}, []string{"b", "c", "d"}},
		{"parent elicitation", input{"parentElicitation", map[string]any{"parentMessageId": "a", "elicitationId": "el"}}, []string{"b", "c", "d"}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			_, path := messageReaderFixture(t, project)
			db, _ := messageReaderFixture(t, project)
			filters := map[string]any{"mode": tc.input.mode}
			for k, v := range tc.input.filters {
				filters[k] = v
			}
			payload, err := json.Marshal(map[string]any{"Component": "messageReader", "DBPath": path, "Filters": filters})
			must(t, err)
			command := exec.Command(legacy)
			command.Stdin = bytes.NewReader(payload)
			raw, err := command.Output()
			must(t, err)
			var before probeResult
			must(t, json.Unmarshal(raw, &before))
			if before.Failed {
				t.Fatalf("legacy: %s", before.Error)
			}
			rt, key := messageReaderRuntime(t, db, "", tc.input.mode, true, true)
			request := messageReadInput(t, tc.input.filters)
			result, err := rt.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "GET", Path: "/v1/internal/agently/message"}}, Input: request})
			must(t, err)
			raw, err = json.Marshal(result.(*read.MessagesOutput).Data)
			must(t, err)
			var rows []json.RawMessage
			must(t, json.Unmarshal(raw, &rows))
			oldRows, newRows := messageComparableRows(t, before.Rows), messageComparableRows(t, rows)
			if !reflect.DeepEqual(oldRows, newRows) {
				t.Fatalf("legacy=%s native=%s", pretty(oldRows), pretty(newRows))
			}
			ids := []string{}
			for _, row := range newRows {
				ids = append(ids, row["id"].(string))
			}
			if !reflect.DeepEqual(ids, tc.expect) {
				t.Fatalf("ids=%v expected=%v", ids, tc.expect)
			}
			if tc.input.mode == "byId" {
				out := result.(*read.MessagesOutput)
				if len(out.Data) != 1 || out.Data[0].Elicitation["prompt"] != "hello" {
					t.Fatal("elicitation hydration lost")
				}
			}
		})
	}
}

func messageReadInput(t *testing.T, filters map[string]any) *read.MessagesInput {
	input := &read.MessagesInput{Has: &read.MessagesInputHas{}}
	value := reflect.ValueOf(input).Elem()
	for i := 0; i < value.NumField(); i++ {
		field := value.Type().Field(i)
		for _, part := range strings.Split(field.Tag.Get("parameter"), ",") {
			if !strings.HasPrefix(part, "in=") {
				continue
			}
			raw, ok := filters[strings.TrimPrefix(part, "in=")]
			if !ok {
				continue
			}
			encoded, err := json.Marshal(raw)
			must(t, err)
			must(t, json.Unmarshal(encoded, value.Field(i).Addr().Interface()))
			value.FieldByName("Has").Elem().FieldByName(field.Name).SetBool(true)
		}
	}
	return input
}
func messageComparableRows(t *testing.T, rows []json.RawMessage) []map[string]any {
	result := normalizeRowsInOrder(t, rows)
	fields := []string{"id", "conversationid", "turnid", "archived", "sequence", "createdat", "updatedat", "createdbyuserid", "status", "mode", "role", "type", "content", "rawcontent", "summary", "contextsummary", "tags", "interim", "elicitationid", "parentmessageid", "supersededby", "linkedconversationid", "attachmentpayloadid", "elicitationpayloadid", "toolname", "embeddingindex", "narration", "iteration", "phase"}
	for i, row := range result {
		if preamble, ok := row["preamble"]; ok {
			row["narration"] = preamble
		}
		selected := map[string]any{}
		for _, field := range fields {
			selected[field] = row[field]
		}
		result[i] = selected
	}
	sort.Slice(result, func(i, j int) bool { return result[i]["id"].(string) < result[j]["id"].(string) })
	return result
}
func messageReaderFixture(t *testing.T, project string) (*sql.DB, string) {
	db, path := goalFixture(t, project)
	_, err := db.Exec(`UPDATE conversation SET visibility='private',created_by_user_id='u1' WHERE id='c1';UPDATE conversation SET visibility='public' WHERE id='c2';
 INSERT INTO turn(id,conversation_id,status) VALUES('t1','c1','running');
 INSERT INTO call_payload(id,kind,mime_type,size_bytes,storage,inline_body) VALUES('p1','elicitation','application/json',18,'inline','{"prompt":"hello"}');
 INSERT INTO message(id,conversation_id,turn_id,role,type,mode,content,preamble,interim,iteration,phase,elicitation_id,parent_message_id,linked_conversation_id,elicitation_payload_id,created_at) VALUES
 ('a','c1','t1','user','task','task','task',NULL,0,0,'intake',NULL,NULL,NULL,NULL,'2026-01-01 00:00:00'),
 ('b','c1','t1','assistant','text','react','working','progress',1,1,'answer','el','a','child','p1','2026-01-02 00:00:00'),
 ('c','c1','t1','assistant','text',NULL,'finished',NULL,0,1,'answer','el','a','child',NULL,'2026-01-03 00:00:00'),
 ('d','c1','t1','tool','elicitation_response',NULL,'response',NULL,0,2,NULL,'el','a','child',NULL,'2026-01-04 00:00:00'),
 ('e','c1','t1','assistant','text','router','route','routing',0,2,NULL,NULL,NULL,NULL,NULL,'2026-01-05 00:00:00'),
 ('other','c2',NULL,'tool','text',NULL,'public',NULL,0,2,NULL,NULL,NULL,NULL,NULL,'2026-01-06 00:00:00');`)
	must(t, err)
	return db, path
}
func messageReaderRuntime(t *testing.T, db *sql.DB, subject, mode string, internal, provided bool) (*druntime.Runtime, spec.Key) {
	resources := resource.New()
	must(t, resources.Register(read.ReaderDatlyResourceNamespace, read.ReaderDatlyResources))
	artifact := payloadArtifact(t, resources, reflect.TypeFor[read.ReaderComponent](), reflect.TypeFor[read.MessagesInput](), reflect.TypeFor[read.MessagesOutput]())
	execution, err := artifact.ReaderCompilation().NewExecution(bootstrap.ReaderRuntimeConfig{SQL: &dsql.SQLComponent{DB: db}})
	must(t, err)
	providers := []locator.Provider{}
	if provided {
		providers = append(providers, ordinaryAccess("messageaccess", func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
			if name == "mode" {
				return mode, true, nil
			}
			return internal, true, nil
		}), provider.Named("visibility", func(context.Context, reflect.Type, string) (any, bool, error) { return &subject, true, nil }))
	}
	rt, err := druntime.NewRuntime([]*registry.RegisteredComponent{{Component: artifact.Component, Input: artifact.Input, Output: artifact.Output, OutputType: reflect.TypeFor[read.MessagesOutput](), Reader: execution, Providers: providers}}, druntime.WithResources(resources))
	must(t, err)
	return rt, artifact.Component.Key
}

func TestMessageReaderScopeSelectorsAndHTTP(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type input struct {
		subject, mode      string
		internal, provided bool
		filters            map[string]any
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
	for _, tc := range []useCase{
		{"anonymous public rows", input{"", "rows", false, true, nil}, expect{ids: []string{"other"}}},
		{"owner rows", input{"u1", "rows", false, true, nil}, expect{ids: []string{"a", "b", "c", "d", "e", "other"}}},
		{"identity cannot bypass private scope", input{"u2", "byId", false, true, map[string]any{"id": "b"}}, expect{ids: []string{}}},
		{"missing host access fails", input{"", "rows", false, false, nil}, expect{failed: true}},
		{"invalid host mode fails", input{"", "unknown", true, true, nil}, expect{failed: true}},
		{"missing lookup identity fails", input{"", "byId", true, true, nil}, expect{failed: true}},
		{"missing elicitation scope fails", input{"", "elicitation", true, true, map[string]any{"elicitationId": "el"}}, expect{failed: true}},
		{"pagination and order", input{"", "rows", true, true, map[string]any{"orderBy": "id ASC", "limit": 2, "offset": 1}}, expect{ids: []string{"b", "c"}}},
		{"projection keeps public narration", input{"", "rows", true, true, map[string]any{"id": "b", "fields": []string{"id", "preamble"}}}, expect{ids: []string{"b"}}},
		{"latest elicitation overrides oversized limit", input{"", "elicitation", true, true, map[string]any{"conversationId": "c1", "elicitationId": "el", "limit": 100}}, expect{ids: []string{"c"}}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			db, _ := messageReaderFixture(t, project)
			rt, key := messageReaderRuntime(t, db, tc.input.subject, tc.input.mode, tc.input.internal, tc.input.provided)
			request := messageReadInput(t, tc.input.filters)
			value, err := rt.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "GET", Path: "/v1/internal/agently/message"}}, Input: request})
			if (err != nil) != tc.expect.failed {
				t.Fatalf("failure=%v expected=%v", err, tc.expect.failed)
			}
			if err != nil {
				return
			}
			ids := []string{}
			for _, row := range value.(*read.MessagesOutput).Data {
				ids = append(ids, row.Id)
				if request.Has.Fields {
					if row.Narration == nil || *row.Narration != "progress" || row.Content != nil || row.Elicitation != nil {
						t.Fatal("projection/narration contract lost")
					}
				}
			}
			sort.Strings(ids)
			if !reflect.DeepEqual(ids, tc.expect.ids) {
				t.Fatalf("ids=%v expected=%v", ids, tc.expect.ids)
			}
		})
	}
	t.Run("HTTP query cannot grant host access or replace mode", func(t *testing.T) {
		db, _ := messageReaderFixture(t, project)
		rt, _ := messageReaderRuntime(t, db, "", "rows", false, true)
		request := httptest.NewRequest("GET", "/v1/internal/agently/message?internal=true&readMode=byId&visibilitySubject=u1", nil)
		scope, err := requestprovider.New(request)
		must(t, err)
		defer scope.Close()
		value, err := rt.ExecuteRoute(context.Background(), "GET", "/v1/internal/agently/message", scope)
		must(t, err)
		rows := value.(*read.MessagesOutput).Data
		if len(rows) != 1 || rows[0].Id != "other" {
			t.Fatal("query replaced host scope")
		}
	})
}

func TestMessageReaderSelectorProxy(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type input struct {
		name, subject string
		selector      state.Selector
	}
	type useCase struct {
		desc   string
		input  input
		expect []string
	}
	for _, tc := range []useCase{
		{"legacy view name forwards pagination and projection", input{"message_rows", "u1", state.Selector{OrderBy: "id ASC", Limit: 1, Offset: 1, Fields: []string{"id", "preamble"}}}, []string{"b"}},
		{"proxy cannot bypass ownership", input{"message", "u2", state.Selector{OrderBy: "id ASC", Limit: 100, Fields: []string{"id"}}}, []string{"other"}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			db, _ := messageReaderFixture(t, project)
			rt, key := messageReaderRuntime(t, db, tc.input.subject, "rows", false, true)
			selectors := state.Selectors{&state.NamedSelector{Name: tc.input.name, Selector: tc.input.selector}}
			proxy := queryselectors.ProviderMapped(selectors, map[string]string{tc.input.name: "reader"})
			selectors[0].Limit = 500
			selectors[0].OrderBy = "untrusted-column"
			result, err := rt.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "GET", Path: "/v1/internal/agently/message"}}, Input: messageReadInput(t, nil), Providers: []locator.Provider{proxy}})
			must(t, err)
			ids := []string{}
			for _, row := range result.(*read.MessagesOutput).Data {
				ids = append(ids, row.Id)
				if row.Content != nil {
					t.Fatal("proxy projection lost")
				}
			}
			if !reflect.DeepEqual(ids, tc.expect) {
				t.Fatalf("ids=%v expected=%v", ids, tc.expect)
			}
		})
	}
}

func TestMessageReaderElicitationPayloadLegacyParity(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	legacy := legacyProbeBinary(t, project)
	var compressed bytes.Buffer
	zip := gzip.NewWriter(&compressed)
	_, err := zip.Write([]byte(`{"prompt":"gzip"}`))
	must(t, err)
	must(t, zip.Close())
	type input struct {
		body        []byte
		compression string
	}
	type useCase struct {
		desc   string
		input  input
		expect string
	}
	for _, tc := range []useCase{{"gzip payload", input{compressed.Bytes(), "gzip"}, "gzip"}, {"malformed gzip is nonfatal", input{[]byte("broken"), "gzip"}, ""}, {"malformed JSON is nonfatal", input{[]byte("broken"), "none"}, ""}, {"empty payload is nonfatal", input{nil, "none"}, ""}} {
		t.Run(tc.desc, func(t *testing.T) {
			oldDB, path := messageReaderFixture(t, project)
			db, _ := messageReaderFixture(t, project)
			for _, fixture := range []*sql.DB{oldDB, db} {
				_, err := fixture.Exec("UPDATE call_payload SET inline_body=?,compression=? WHERE id='p1'", tc.input.body, tc.input.compression)
				must(t, err)
			}
			payload, err := json.Marshal(map[string]any{"Component": "messageReader", "DBPath": path, "Filters": map[string]any{"mode": "byId", "id": "b"}})
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
			rt, key := messageReaderRuntime(t, db, "", "byId", true, true)
			result, err := rt.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "GET", Path: "/v1/internal/agently/message"}}, Input: messageReadInput(t, map[string]any{"id": "b"})})
			must(t, err)
			rows := result.(*read.MessagesOutput).Data
			if len(rows) != 1 {
				t.Fatal("missing row")
			}
			var original map[string]any
			must(t, json.Unmarshal(before.Rows[0], &original))
			oldValue := original["Elicitation"]
			if value, ok := original["elicitation"]; ok {
				oldValue = value
			}
			raw, err = json.Marshal(rows[0].Elicitation)
			must(t, err)
			var newValue any
			must(t, json.Unmarshal(raw, &newValue))
			if !reflect.DeepEqual(oldValue, newValue) {
				t.Fatalf("legacy elicitation=%v native=%v", oldValue, newValue)
			}
			if tc.expect != "" && rows[0].Elicitation["prompt"] != tc.expect {
				t.Fatal("gzip hydration lost")
			}
		})
	}
}

func TestMessageReaderTranscriptLegacyParity(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	legacy := legacyProbeBinary(t, project)
	type input struct {
		id          string
		model, tool bool
	}
	type useCase struct {
		desc   string
		input  input
		expect bool
	}
	for _, tc := range []useCase{{"assistant without optional facts", input{"b", false, false}, false}, {"assistant model facts and five payload relations", input{"b", true, false}, true}, {"assistant includes both fact families", input{"b", true, true}, true}, {"user owns tool message children", input{"a", false, true}, false}, {"tool message owns tool call and payloads", input{"d", false, true}, false}, {"tool facts disabled", input{"d", false, false}, false}} {
		t.Run(tc.desc, func(t *testing.T) {
			_, path := messageTranscriptFixture(t, project)
			db, _ := messageTranscriptFixture(t, project)
			filters := map[string]any{"mode": "transcript", "id": tc.input.id, "includeModelCall": tc.input.model, "includeToolCall": tc.input.tool}
			payload, err := json.Marshal(map[string]any{"Component": "messageReader", "DBPath": path, "Filters": filters})
			must(t, err)
			command := exec.Command(legacy)
			command.Stdin = bytes.NewReader(payload)
			raw, err := command.Output()
			must(t, err)
			var before probeResult
			must(t, json.Unmarshal(raw, &before))
			if before.Failed {
				t.Fatalf("legacy: %s", before.Error)
			}
			rt, key := messageReaderRuntime(t, db, "", "transcript", true, true)
			result, err := rt.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "GET", Path: "/v1/internal/agently/message"}}, Input: messageReadInput(t, filters)})
			must(t, err)
			raw, err = json.Marshal(result.(*read.MessagesOutput).Data)
			must(t, err)
			var rows []json.RawMessage
			must(t, json.Unmarshal(raw, &rows))
			oldRows, newRows := normalizeConversationRows(t, before.Rows), normalizeConversationRows(t, rows)
			// The canonical reader has extra internal payload backing metadata; compare
			// every field of the original transcript contract recursively.
			for i, row := range oldRows {
				for field, value := range row {
					if !reflect.DeepEqual(value, newRows[i][field]) {
						t.Fatalf("field %s legacy=%s native=%s", field, pretty(value), pretty(newRows[i][field]))
					}
				}
			}
			native := result.(*read.MessagesOutput).Data
			if len(native) != 1 {
				t.Fatal("missing message")
			}
			if (native[0].ModelCall != nil) != tc.expect {
				t.Fatalf("model call present=%v expected=%v", native[0].ModelCall != nil, tc.expect)
			}
		})
	}
}
func messageTranscriptFixture(t *testing.T, project string) (*sql.DB, string) {
	db, path := messageReaderFixture(t, project)
	_, err := db.Exec(`INSERT INTO conversation(id,status,created_at) VALUES('child','running','2026-01-01 00:00:00');
 UPDATE message SET attachment_payload_id='p1' WHERE id='d';
 INSERT INTO model_call(message_id,provider,model,model_kind,status,trace_id,request_payload_id,response_payload_id,provider_request_payload_id,provider_response_payload_id,stream_payload_id) VALUES('b','p','m','chat','thinking','trace','p1','p1','p1','p1','p1');
 INSERT INTO tool_call(message_id,turn_id,op_id,tool_name,tool_kind,status,trace_id,request_payload_id,response_payload_id) VALUES('d','t1','op','tool','function','completed','trace','p1','p1');`)
	must(t, err)
	return db, path
}

func TestMessageReaderTranscriptRelationScope(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	db, _ := messageTranscriptFixture(t, project)
	_, err := db.Exec(`UPDATE conversation SET visibility='private',created_by_user_id='u2' WHERE id IN ('child','c2');
 INSERT INTO model_call(message_id,provider,model,model_kind,status,trace_id) VALUES('other','p','foreign','chat','completed','trace');
 INSERT INTO tool_call(message_id,op_id,tool_name,tool_kind,status,trace_id) VALUES('other','foreign','tool','function','completed','trace');
 UPDATE message SET parent_message_id='a' WHERE id='other';`)
	must(t, err)
	type useCase struct {
		desc, id    string
		expectLinks int
	}
	for _, tc := range []useCase{{"linked private conversation and trace links stay scoped", "b", 1}, {"foreign child message stays scoped", "a", 0}} {
		t.Run(tc.desc, func(t *testing.T) {
			rt, key := messageReaderRuntime(t, db, "u1", "transcript", false, true)
			result, err := rt.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "GET", Path: "/v1/internal/agently/message"}}, Input: messageReadInput(t, map[string]any{"id": tc.id, "includeModelCall": true, "includeToolCall": true})})
			must(t, err)
			rows := result.(*read.MessagesOutput).Data
			if len(rows) != 1 {
				t.Fatal("missing authorized parent")
			}
			row := rows[0]
			if row.LinkedConversation != nil {
				t.Fatal("private linked conversation leaked")
			}
			if tc.id == "b" {
				if row.ModelCall == nil || len(row.ModelCall.ToolCallLinks) != tc.expectLinks {
					t.Fatal("cross-conversation trace link leaked")
				}
			}
			if tc.id == "a" {
				for _, child := range row.ToolMessage {
					if child.Id == "other" {
						t.Fatal("foreign private child leaked")
					}
				}
			}
		})
	}
}
