package tests

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/viant/agently-core/internal/datly/queryselectors"
	read "github.com/viant/agently-core/internal/datly/runsteps/read"
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

func TestRunStepsReaderLegacyParity(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type input struct{ filters map[string]any }
	type expect struct {
		ids []string
	}
	type useCase struct {
		desc   string
		input  input
		expect expect
	}
	for _, tc := range []useCase{
		{"all model and tool steps", input{nil}, expect{ids: []string{"m1", "m2", "other", "public", "t1", "t2"}}},
		{"one run includes both fact tables", input{map[string]any{"runId": "r1"}}, expect{ids: []string{"m1", "m2", "t1", "t2"}}},
		{"foreign run", input{map[string]any{"runId": "r2"}}, expect{ids: []string{"other"}}},
		{"provided zero iteration", input{map[string]any{"runId": "r1", "iteration": 0}}, expect{ids: []string{"m1", "m2", "t1"}}},
		{"nonzero iteration", input{map[string]any{"iteration": 1}}, expect{ids: []string{"t2"}}},
		{"before cursor", input{map[string]any{"runId": "r1", "cursorBefore": "t1"}}, expect{ids: []string{"m1", "m2"}}},
		{"after cursor", input{map[string]any{"runId": "r1", "cursorAfter": "m2"}}, expect{ids: []string{"t1", "t2"}}},
		{"unknown run", input{map[string]any{"runId": "absent"}}, expect{ids: []string{}}},
		{"model type filter", input{map[string]any{"stepTypes": []string{"model_call"}}}, expect{ids: []string{"m1", "m2", "other", "public"}}},
		{"tool type filter", input{map[string]any{"stepTypes": []string{"tool_call"}}}, expect{ids: []string{"t1", "t2"}}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			db, _ := runStepsFixture(t, project)
			rt, key := runStepsReaderRuntime(t, db, "", true, true)
			request := runStepsInput(t, tc.input.filters)
			out, err := rt.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "GET", Path: "/v1/internal/agently/run/steps"}}, Input: request})
			must(t, err)
			raw, err := json.Marshal(out.(*read.RunStepsOutput).Data)
			must(t, err)
			var rows []json.RawMessage
			must(t, json.Unmarshal(raw, &rows))
			newRows := normalizeRowsInOrder(t, rows)
			sort.Slice(newRows, func(i, j int) bool { return newRows[i]["messageid"].(string) < newRows[j]["messageid"].(string) })
			ids := []string{}
			for _, row := range newRows {
				ids = append(ids, row["messageid"].(string))
			}
			if !reflect.DeepEqual(ids, tc.expect.ids) {
				t.Fatalf("ids=%v expected=%v", ids, tc.expect.ids)
			}
		})
	}
}
func runStepsFixture(t *testing.T, project string) (*sql.DB, string) {
	db, path := goalFixture(t, project)
	_, err := db.Exec(`UPDATE conversation SET visibility='private',created_by_user_id='u1' WHERE id='c1';UPDATE conversation SET visibility='private',created_by_user_id='u2' WHERE id='c2';UPDATE conversation SET visibility='public' WHERE id='c3';
 INSERT INTO run(id,conversation_id,effective_user_id,status) VALUES('r1','c1','u1','running'),('r2','c2','u2','running'),('r3','c3',NULL,'running');
 INSERT INTO message(id,conversation_id,role,type) VALUES('m1','c1','assistant','text'),('m2','c1','assistant','text'),('t1','c1','tool','tool_op'),('t2','c1','tool','tool_op'),('other','c2','assistant','text'),('public','c3','assistant','text');
 INSERT INTO model_call(message_id,provider,model,model_kind,status,run_id,iteration,started_at) VALUES('m1','p','model1','chat','thinking','r1',0,'2026-01-01 00:00:00'),('m2','p','model2','chat','completed','r1',0,'2026-01-02 00:00:00'),('other','p','private','chat','completed','r2',0,'2026-01-03 00:00:00'),('public','p','public','chat','completed','r3',0,'2026-01-04 00:00:00');
 INSERT INTO tool_call(message_id,op_id,tool_name,tool_kind,status,run_id,iteration,started_at) VALUES('t1','op1','tool1','function','completed','r1',0,'2026-01-05 00:00:00'),('t2','op2','tool2','function','failed','r1',1,'2026-01-06 00:00:00');`)
	must(t, err)
	return db, path
}
func runStepsInput(t *testing.T, filters map[string]any) *read.RunStepsInput {
	input := &read.RunStepsInput{Has: &read.RunStepsInputHas{}}
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
func runStepsReaderRuntime(t *testing.T, db *sql.DB, subject string, internal, provided bool) (*druntime.Runtime, spec.Key) {
	resources := resource.New()
	must(t, resources.Register(read.ReaderDatlyResourceNamespace, read.ReaderDatlyResources))
	artifact := payloadArtifact(t, resources, reflect.TypeFor[read.ReaderComponent](), reflect.TypeFor[read.RunStepsInput](), reflect.TypeFor[read.RunStepsOutput]())
	execution, err := artifact.ReaderCompilation().NewExecution(bootstrap.ReaderRuntimeConfig{SQL: &dsql.SQLComponent{DB: db}})
	must(t, err)
	providers := []locator.Provider{}
	if provided {
		providers = append(providers, ordinaryAccess("runaccess", func(context.Context, reflect.Type, string) (any, bool, error) {
			return internal, true, nil
		}), provider.Named("visibility", func(context.Context, reflect.Type, string) (any, bool, error) { return &subject, true, nil }))
	}
	rt, err := druntime.NewRuntime([]*registry.RegisteredComponent{{Component: artifact.Component, Input: artifact.Input, Output: artifact.Output, OutputType: reflect.TypeFor[read.RunStepsOutput](), Reader: execution, Providers: providers}}, druntime.WithResources(resources))
	must(t, err)
	return rt, artifact.Component.Key
}

func TestRunStepsReaderScopeSelectorsAndHTTP(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type input struct {
		subject            string
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
		{"anonymous only public steps", input{"", false, true, nil}, expect{ids: []string{"public"}}},
		{"owner sees own and public steps", input{"u1", false, true, nil}, expect{ids: []string{"m1", "m2", "public", "t1", "t2"}}},
		{"other owner sees separate run", input{"u2", false, true, nil}, expect{ids: []string{"other", "public"}}},
		{"run filter cannot bypass scope", input{"u1", false, true, map[string]any{"runId": "r2"}}, expect{ids: []string{}}},
		{"provided zero iteration", input{"", true, true, map[string]any{"iteration": 0}}, expect{ids: []string{"m1", "m2", "other", "public", "t1"}}},
		{"missing host authority fails", input{"", false, false, nil}, expect{failed: true}},
		{"paginated order is stable", input{"u1", false, true, map[string]any{"orderBy": "started_at DESC", "limit": 1, "offset": 1}}, expect{ids: []string{"t1"}}},
		{"projection loads declared identity only", input{"u1", false, true, map[string]any{"fields": []string{"message_id"}}}, expect{ids: []string{"m1", "m2", "public", "t1", "t2"}}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			db, _ := runStepsFixture(t, project)
			rt, key := runStepsReaderRuntime(t, db, tc.input.subject, tc.input.internal, tc.input.provided)
			request := runStepsInput(t, tc.input.filters)
			out, err := rt.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "GET", Path: "/v1/internal/agently/run/steps"}}, Input: request})
			if (err != nil) != tc.expect.failed {
				t.Fatalf("failure=%v expected=%v", err, tc.expect.failed)
			}
			if err != nil {
				return
			}
			ids := []string{}
			for _, row := range out.(*read.RunStepsOutput).Data {
				ids = append(ids, row.MessageId)
				if request.Has.Fields && (row.Name != "" || row.Status != "") {
					t.Fatal("projection lost")
				}
			}
			sort.Strings(ids)
			if !reflect.DeepEqual(ids, tc.expect.ids) {
				t.Fatalf("ids=%v expected=%v", ids, tc.expect.ids)
			}
		})
	}
	t.Run("HTTP query cannot grant internal access", func(t *testing.T) {
		db, _ := runStepsFixture(t, project)
		rt, _ := runStepsReaderRuntime(t, db, "u1", false, true)
		req := httptest.NewRequest("GET", "/v1/internal/agently/run/steps?runId=r2&internal=true&visibilitySubject=u2", nil)
		scope, err := requestprovider.New(req)
		must(t, err)
		defer scope.Close()
		out, err := rt.ExecuteRoute(context.Background(), "GET", "/v1/internal/agently/run/steps", scope)
		must(t, err)
		if len(out.(*read.RunStepsOutput).Data) != 0 {
			t.Fatal("query bypassed host scope")
		}
	})
	t.Run("legacy selector name forwards with scope", func(t *testing.T) {
		db, _ := runStepsFixture(t, project)
		rt, key := runStepsReaderRuntime(t, db, "u1", false, true)
		proxy := queryselectors.ProviderMapped(state.Selectors{&state.NamedSelector{Name: "RunSteps", Selector: state.Selector{Fields: []string{"message_id"}, OrderBy: "message_id ASC", Limit: 2}}}, map[string]string{"RunSteps": "reader"})
		out, err := rt.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "GET", Path: "/v1/internal/agently/run/steps"}}, Input: runStepsInput(t, nil), Providers: []locator.Provider{proxy}})
		must(t, err)
		rows := out.(*read.RunStepsOutput).Data
		if len(rows) != 2 || rows[0].MessageId != "m1" || rows[1].MessageId != "m2" || rows[0].Name != "" {
			t.Fatalf("proxy lost scope or projection: %s", pretty(rows))
		}
	})
}
