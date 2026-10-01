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

	"github.com/viant/agently-core/internal/datly/queryselectors"
	read "github.com/viant/agently-core/internal/datly/toolapprovalqueue/read"
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

func TestApprovalReaderLegacyParity(t *testing.T) {
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
		{"list order", input{"rows", nil}, []string{"other", "failed", "timeout", "executed", "rejected", "approved", "existing"}},
		{"owner list", input{"rows", map[string]any{"userId": "u1"}}, []string{"failed", "timeout", "executed", "rejected", "approved", "existing"}},
		{"conversation filter", input{"rows", map[string]any{"conversationId": "c1"}}, []string{"failed", "timeout", "executed", "rejected", "approved", "existing"}},
		{"turn filter", input{"rows", map[string]any{"turnId": "t1"}}, []string{"existing"}},
		{"message filter", input{"rows", map[string]any{"messageId": "m1"}}, []string{"existing"}},
		{"tool filter", input{"rows", map[string]any{"toolName": "shell/exec"}}, []string{"failed", "timeout", "executed", "rejected", "approved"}},
		{"status filter", input{"rows", map[string]any{"status": "pending"}}, []string{"existing"}},
		{"id filter", input{"rows", map[string]any{"id": "approved"}}, []string{"approved"}},
		{"provided blank id", input{"rows", map[string]any{"id": ""}}, []string{}},
		{"outcome transition order includes NULL first", input{"outcome", nil}, []string{"other", "rejected", "timeout", "approved", "executed", "failed"}},
		{"outcome user scope", input{"outcome", map[string]any{"userId": "u1"}}, []string{"rejected", "timeout", "approved", "executed", "failed"}},
		{"outcome conversation scope", input{"outcome", map[string]any{"conversationId": "c2"}}, []string{"other"}},
		{"outcome since excludes NULL", input{"outcome", map[string]any{"since": "2026-01-02T12:00:00Z"}}, []string{"timeout", "approved", "executed", "failed"}},
		{"outcome until", input{"outcome", map[string]any{"until": "2026-01-04T12:00:00Z"}}, []string{"rejected", "timeout", "approved"}},
		{"outcome window", input{"outcome", map[string]any{"since": "2026-01-02T12:00:00Z", "until": "2026-01-04T12:00:00Z"}}, []string{"timeout", "approved"}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			_, path := approvalReaderFixture(t, project)
			db, _ := approvalReaderFixture(t, project)
			filters := map[string]any{"mode": tc.input.mode}
			for k, v := range tc.input.filters {
				filters[k] = v
			}
			payload, err := json.Marshal(map[string]any{"Component": "approvalReader", "DBPath": path, "Filters": filters})
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
			rt, key := approvalReaderRuntime(t, db, "", tc.input.mode, true, true)
			result, err := rt.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "GET", Path: "/v1/internal/agently/tool-approval"}}, Input: approvalReadInput(t, tc.input.filters)})
			must(t, err)
			raw, err = json.Marshal(result.(*read.ApprovalRowsOutput).Data)
			must(t, err)
			var rows []json.RawMessage
			must(t, json.Unmarshal(raw, &rows))
			oldRows, newRows := normalizeRowsInOrder(t, before.Rows), normalizeRowsInOrder(t, rows)
			if tc.input.mode == "rows" {
				for _, row := range newRows {
					delete(row, "transitionat")
				}
			}
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
		})
	}
}
func approvalReaderFixture(t *testing.T, project string) (*sql.DB, string) {
	db, path := approvalFixture(t, project)
	_, err := db.Exec(`INSERT INTO tool_approval_queue(id,user_id,conversation_id,tool_name,arguments,status,created_at,approved_at,executed_at,timed_out_at,updated_at) VALUES
 ('approved','u1','c1','shell/exec',X'7B7D','approved','2026-01-02 00:00:00','2026-01-04 00:00:00',NULL,NULL,NULL),
 ('rejected','u1','c1','shell/exec',X'7B7D','rejected','2026-01-03 00:00:00','2026-01-02 00:00:00',NULL,NULL,NULL),
 ('executed','u1','c1','shell/exec',X'7B7D','executed','2026-01-04 00:00:00',NULL,'2026-01-05 00:00:00',NULL,NULL),
 ('timeout','u1','c1','shell/exec',X'7B7D','timed_out','2026-01-05 00:00:00',NULL,NULL,'2026-01-03 00:00:00',NULL),
 ('failed','u1','c1','shell/exec',X'7B7D','failed','2026-01-06 00:00:00',NULL,NULL,NULL,'2026-01-06 00:00:00'),
 ('other','u2','c2','other',X'7B7D','approved','2026-01-07 00:00:00',NULL,NULL,NULL,'2026-01-07 00:00:00');`)
	must(t, err)
	return db, path
}

func TestApprovalReaderScopeSelectorsAndHTTP(t *testing.T) {
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
		{"anonymous cannot read personal queue", input{"", "rows", false, true, nil}, expect{ids: []string{}}},
		{"owner sees own queue", input{"u2", "rows", false, true, nil}, expect{ids: []string{"other"}}},
		{"requested owner cannot bypass host identity", input{"u2", "rows", false, true, map[string]any{"userId": "u1"}}, expect{ids: []string{}}},
		{"outcome scope", input{"u2", "outcome", false, true, nil}, expect{ids: []string{"other"}}},
		{"missing host scope fails", input{"", "rows", false, false, nil}, expect{failed: true}},
		{"unknown host mode fails", input{"", "bad", true, true, nil}, expect{failed: true}},
		{"pagination and projection", input{"u1", "rows", false, true, map[string]any{"orderBy": "id ASC", "limit": 1, "offset": 1, "fields": []string{"id"}}}, expect{ids: []string{"executed"}}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			db, _ := approvalReaderFixture(t, project)
			rt, key := approvalReaderRuntime(t, db, tc.input.subject, tc.input.mode, tc.input.internal, tc.input.provided)
			request := approvalReadInput(t, tc.input.filters)
			out, err := rt.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "GET", Path: "/v1/internal/agently/tool-approval"}}, Input: request})
			if (err != nil) != tc.expect.failed {
				t.Fatalf("failure=%v expected=%v", err, tc.expect.failed)
			}
			if err != nil {
				return
			}
			ids := []string{}
			for _, row := range out.(*read.ApprovalRowsOutput).Data {
				ids = append(ids, row.Id)
				if request.Has.Fields && (row.ToolName != "" || row.Arguments != nil) {
					t.Fatal("projection lost")
				}
			}
			if !reflect.DeepEqual(ids, tc.expect.ids) {
				t.Fatalf("ids=%v expected=%v", ids, tc.expect.ids)
			}
		})
	}
	t.Run("HTTP query cannot override host identity", func(t *testing.T) {
		db, _ := approvalReaderFixture(t, project)
		rt, _ := approvalReaderRuntime(t, db, "u2", "rows", false, true)
		request := httptest.NewRequest("GET", "/v1/internal/agently/tool-approval?internal=true&visibilitySubject=u1&readMode=outcome", nil)
		scope, err := requestprovider.New(request)
		must(t, err)
		defer scope.Close()
		out, err := rt.ExecuteRoute(context.Background(), "GET", "/v1/internal/agently/tool-approval", scope)
		must(t, err)
		rows := out.(*read.ApprovalRowsOutput).Data
		if len(rows) != 1 || rows[0].Id != "other" {
			t.Fatal("host identity overridden")
		}
	})
	t.Run("legacy selector name forwards safely", func(t *testing.T) {
		db, _ := approvalReaderFixture(t, project)
		rt, key := approvalReaderRuntime(t, db, "u2", "rows", false, true)
		proxy := queryselectors.ProviderMapped(state.Selectors{&state.NamedSelector{Name: "queue_rows", Selector: state.Selector{OrderBy: "id ASC", Limit: 100, Fields: []string{"id"}}}}, map[string]string{"queue_rows": "reader"})
		out, err := rt.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "GET", Path: "/v1/internal/agently/tool-approval"}}, Input: approvalReadInput(t, nil), Providers: []locator.Provider{proxy}})
		must(t, err)
		rows := out.(*read.ApprovalRowsOutput).Data
		if len(rows) != 1 || rows[0].Id != "other" || rows[0].Arguments != nil {
			t.Fatal("proxy lost scope/projection")
		}
	})
}
func approvalReaderRuntime(t *testing.T, db *sql.DB, subject, mode string, internal, provided bool) (*druntime.Runtime, spec.Key) {
	resources := resource.New()
	must(t, resources.Register(read.ReaderDatlyResourceNamespace, read.ReaderDatlyResources))
	artifact := payloadArtifact(t, resources, reflect.TypeFor[read.ReaderComponent](), reflect.TypeFor[read.ApprovalRowsInput](), reflect.TypeFor[read.ApprovalRowsOutput]())
	execution, err := artifact.ReaderCompilation().NewExecution(bootstrap.ReaderRuntimeConfig{SQL: &dsql.SQLComponent{DB: db}})
	must(t, err)
	providers := []locator.Provider{}
	if provided {
		providers = append(providers, ordinaryAccess("approvalaccess", func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
			if name == "mode" {
				return mode, true, nil
			}
			return internal, true, nil
		}), provider.Named("visibility", func(context.Context, reflect.Type, string) (any, bool, error) { return &subject, true, nil }))
	}
	rt, err := druntime.NewRuntime([]*registry.RegisteredComponent{{Component: artifact.Component, Input: artifact.Input, Output: artifact.Output, OutputType: reflect.TypeFor[read.ApprovalRowsOutput](), Reader: execution, Providers: providers}}, druntime.WithResources(resources))
	must(t, err)
	return rt, artifact.Component.Key
}
func approvalReadInput(t *testing.T, filters map[string]any) *read.ApprovalRowsInput {
	input := &read.ApprovalRowsInput{Has: &read.ApprovalRowsInputHas{}}
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
