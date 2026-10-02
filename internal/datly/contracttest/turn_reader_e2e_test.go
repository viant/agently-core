package tests

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	read "github.com/viant/agently-core/internal/datly/turn/read"
	"github.com/viant/bindly/locator"
	"github.com/viant/bindly/resource"
	"github.com/viant/datly/bootstrap"
	dexec "github.com/viant/datly/exec"
	druntime "github.com/viant/datly/runtime"
	"github.com/viant/datly/runtime/registry"
	"github.com/viant/datly/spec"
	dsql "github.com/viant/datly/sql"
)

func TestTurnReaderLegacyParity(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type input struct {
		mode    string
		filters map[string]any
	}
	type useCase struct {
		desc   string
		input  input
		expect []string
	}
	scope := func(extra map[string]any) map[string]any {
		result := map[string]any{"conversationId": "c1"}
		for k, v := range extra {
			result[k] = v
		}
		return result
	}
	for _, tc := range []useCase{
		{"ordered list", input{"rows", scope(nil)}, []string{"f", "d", "c", "b", "a"}},
		{"lookup exact identity", input{"byId", scope(map[string]any{"id": "b"})}, []string{"b"}},
		{"lookup conflict retains conversation filter", input{"byId", scope(map[string]any{"id": "e"})}, []string{}},
		{"latest active tie uses identity", input{"active", scope(nil)}, []string{"f"}},
		{"active scope separates conversations", input{"active", map[string]any{"conversationId": "c2"}}, []string{"e"}},
		{"queued null sequence is first", input{"nextQueued", scope(nil)}, []string{"a"}},
		{"queued list preserves sequence time and identity ties", input{"queued", scope(nil)}, []string{"a", "b", "c"}},
		{"queued misses empty conversation", input{"queued", map[string]any{"conversationId": ""}}, []string{}},
		{"missing conversation", input{"rows", map[string]any{"conversationId": "absent"}}, []string{}},
		{"supplied empty identity remains active", input{"rows", scope(map[string]any{"id": ""})}, []string{}},
		{"status IN predicate", input{"rows", scope(map[string]any{"statuses": []string{"running"}})}, []string{"f"}},
		{"time lower bound", input{"rows", scope(map[string]any{"createdSince": "2026-01-01T12:00:00Z"})}, []string{"f", "d", "c", "b"}},
		{"time upper bound", input{"rows", scope(map[string]any{"createdBefore": "2026-01-02T12:00:00Z"})}, []string{"c", "b", "a"}},
		{"cursor before honors ID tie", input{"rows", scope(map[string]any{"cursorBefore": "c"})}, []string{"b", "a"}},
		{"cursor after honors ID tie", input{"rows", scope(map[string]any{"cursorAfter": "c"})}, []string{"f", "d"}},
		{"missing cursor yields no rows", input{"rows", scope(map[string]any{"cursorBefore": "absent"})}, []string{}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			db, _ := turnReaderFixture(t, project)
			rt, key := turnReaderRuntime(t, db, tc.input.mode, true)
			input := &read.TurnRowsInput{Has: &read.TurnRowsInputHas{}}
			for _, field := range []struct {
				name  string
				value *string
				has   *bool
			}{{"conversationId", &input.ConversationID, &input.Has.ConversationID}, {"id", &input.TurnId, &input.Has.TurnId}, {"cursorBefore", &input.CursorBefore, &input.Has.CursorBefore}, {"cursorAfter", &input.CursorAfter, &input.Has.CursorAfter}} {
				if value, ok := tc.input.filters[field.name]; ok {
					data, err := json.Marshal(value)
					must(t, err)
					must(t, json.Unmarshal(data, field.value))
					*field.has = true
				}
			}
			if value, ok := tc.input.filters["statuses"]; ok {
				data, err := json.Marshal(value)
				must(t, err)
				must(t, json.Unmarshal(data, &input.Statuses))
				input.Has.Statuses = true
			}
			for _, field := range []struct {
				name  string
				value *time.Time
				has   *bool
			}{{"createdSince", &input.CreatedSince, &input.Has.CreatedSince}, {"createdBefore", &input.CreatedBefore, &input.Has.CreatedBefore}} {
				if value, ok := tc.input.filters[field.name]; ok {
					data, err := json.Marshal(value)
					must(t, err)
					must(t, json.Unmarshal(data, field.value))
					*field.has = true
				}
			}
			value, err := rt.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "GET", Path: "/v1/api/agently/turn/list/list"}}, Input: input})
			must(t, err)
			raw, err := json.Marshal(value.(*read.TurnRowsOutput).Data)
			must(t, err)
			var rows []json.RawMessage
			must(t, json.Unmarshal(raw, &rows))
			newRows := normalizeRowsInOrder(t, rows)
			if tc.input.mode == "queued" {
				for i, row := range newRows {
					if row["id"] == "a" && row["queueseq"] != nil || row["id"] != "a" && row["queueseq"] != float64(0) {
						t.Fatalf("queue sequence for %s=%v", row["id"], row["queueseq"])
					}
					newRows[i] = map[string]any{"id": row["id"], "queueseq": row["queueseq"]}
				}
			}
			ids := []string{}
			for _, row := range newRows {
				ids = append(ids, row["id"].(string))
			}
			if !reflect.DeepEqual(ids, tc.expect) {
				t.Fatalf("ids=%v expected=%v", ids, tc.expect)
			}
		})
	}
}

func turnReaderFixture(t *testing.T, project string) (*sql.DB, string) {
	db, path := goalFixture(t, project)
	_, err := db.Exec(`INSERT INTO turn(id,conversation_id,status,queue_seq,created_at) VALUES
 ('a','c1','queued',NULL,'2026-01-01 00:00:00'),
 ('b','c1','queued',0,'2026-01-02 00:00:00'),
 ('c','c1','queued',0,'2026-01-02 00:00:00'),
 ('d','c1','waiting_for_user',1,'2026-01-03 00:00:00'),
 ('f','c1','running',2,'2026-01-03 00:00:00'),
 ('e','c2','running',3,'2026-01-04 00:00:00')`)
	must(t, err)
	return db, path
}
func turnReaderRuntime(t *testing.T, db *sql.DB, mode string, provided bool) (*druntime.Runtime, spec.Key) {
	resources := resource.New()
	must(t, resources.Register(read.ReaderDatlyResourceNamespace, read.ReaderDatlyResources))
	artifact := payloadArtifact(t, resources, reflect.TypeFor[read.ReaderComponent](), reflect.TypeFor[read.TurnRowsInput](), reflect.TypeFor[read.TurnRowsOutput]())
	reader, err := artifact.ReaderCompilation().NewExecution(bootstrap.ReaderRuntimeConfig{SQL: &dsql.SQLComponent{DB: db}})
	must(t, err)
	providers := []locator.Provider{}
	if provided {
		providers = append(providers, ordinaryAccess("turnaccess", func(context.Context, reflect.Type, string) (any, bool, error) { return mode, true, nil }))
	}
	rt, err := druntime.NewRuntime([]*registry.RegisteredComponent{{Component: artifact.Component, Input: artifact.Input, Output: artifact.Output, OutputType: reflect.TypeFor[read.TurnRowsOutput](), Reader: reader, Providers: providers}}, druntime.WithResources(resources))
	must(t, err)
	return rt, artifact.Component.Key
}

func TestTurnReaderScopeAndSelectors(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type input struct {
		mode     string
		provided bool
		request  *read.TurnRowsInput
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
		{"missing host mode fails closed", input{"", false, &read.TurnRowsInput{}}, expect{failed: true}},
		{"unsupported host mode fails closed", input{"unknown", true, &read.TurnRowsInput{}}, expect{failed: true}},
		{"native selector pagination", input{"rows", true, &read.TurnRowsInput{ConversationID: "c1", Limit: 2, Offset: 1, Has: &read.TurnRowsInputHas{ConversationID: true, Limit: true, Offset: true}}}, expect{ids: []string{"d", "c"}}},
		{"queued projection avoids metadata overfetch", input{"queued", true, &read.TurnRowsInput{ConversationID: "c1", Has: &read.TurnRowsInputHas{ConversationID: true}}}, expect{ids: []string{"a", "b", "c"}}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			db, _ := turnReaderFixture(t, project)
			rt, key := turnReaderRuntime(t, db, tc.input.mode, tc.input.provided)
			value, err := rt.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "GET", Path: "/v1/api/agently/turn/list/list"}}, Input: tc.input.request})
			if (err != nil) != tc.expect.failed {
				t.Fatalf("failure=%v expected=%v", err, tc.expect.failed)
			}
			if err != nil {
				return
			}
			rows := value.(*read.TurnRowsOutput).Data
			ids := []string{}
			for _, row := range rows {
				ids = append(ids, row.Id)
				if tc.input.mode == "queued" && (row.ConversationId != "" || row.Status != "" || !row.CreatedAt.IsZero()) {
					t.Fatal("queued projection fetched unrelated metadata")
				}
			}
			if !reflect.DeepEqual(ids, tc.expect.ids) {
				t.Fatalf("ids=%v expected=%v", ids, tc.expect.ids)
			}
		})
	}
}
