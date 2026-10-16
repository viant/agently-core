package tests

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	cube "github.com/viant/agently-core/internal/datly/run/cube"
	"github.com/viant/bindly/locator"
	requestprovider "github.com/viant/bindly/provider/request"
	"github.com/viant/bindly/resource"
	"github.com/viant/datly/bootstrap"
	"github.com/viant/datly/report"
	druntime "github.com/viant/datly/runtime"
	provider "github.com/viant/datly/runtime/handler/provider"
	dsql "github.com/viant/datly/sql"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestRunFactCubeSQLite(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	db, _ := schedulerRunListFixture(t, project)
	_, err := db.Exec(`UPDATE run SET usage_total_tokens=10,usage_cost=1.5 WHERE id='pub-new';UPDATE run SET usage_total_tokens=NULL,usage_cost=NULL WHERE id='pub-old'`)
	must(t, err)
	type input struct {
		subject, mode string
		internal      bool
		body          string
	}
	type useCase struct {
		desc   string
		input  input
		expect []map[string]any
	}
	for _, tc := range []useCase{
		{"anonymous scheduler count", input{mode: "scheduler", internal: true, body: `{"measures":["RecordCount"]}`}, []map[string]any{{"recordcount": float64(2)}}},
		{"owner scheduler count", input{subject: "u1", mode: "scheduler", internal: true, body: `{"measures":["RecordCount"]}`}, []map[string]any{{"recordcount": float64(3)}}},
		{"status groups keep schedule visibility", input{subject: "u1", mode: "scheduler", internal: true, body: `{"dimensions":["status"],"measures":["RecordCount"],"orderBy":["status"]}`}, []map[string]any{{"status": "failed", "recordcount": float64(1)}, {"status": "running", "recordcount": float64(2)}}},
		{"aggregate sums retain nullable measures", input{mode: "scheduler", internal: true, body: `{"measures":["RecordCount","TotalTokens","TotalCost"]}`}, []map[string]any{{"recordcount": float64(2), "totaltokens": float64(10), "totalcost": float64(1.5)}}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			rt := runCubeRuntime(t, db, tc.input.subject, tc.input.mode, tc.input.internal)
			var requested map[string]any
			must(t, json.Unmarshal([]byte(tc.input.body), &requested))
			for _, section := range []string{"dimensions", "measures"} {
				if values, ok := requested[section].([]any); ok {
					selected := map[string]bool{}
					for _, value := range values {
						selected[value.(string)] = true
					}
					requested[section] = selected
				}
			}
			body, err := json.Marshal(requested)
			must(t, err)
			request := httptest.NewRequest("POST", "/v1/internal/agently/run/report/cube", strings.NewReader(string(body)))
			request.Header.Set("Content-Type", "application/json")
			scope, err := requestprovider.New(request)
			must(t, err)
			defer scope.Close()
			value, err := rt.ExecuteRoute(context.Background(), "POST", "/v1/internal/agently/run/report/cube", scope)
			must(t, err)
			raw, err := json.Marshal(value)
			must(t, err)
			var envelope map[string]json.RawMessage
			must(t, json.Unmarshal(raw, &envelope))
			var rows []map[string]any
			data, ok := envelope["Data"]
			if !ok {
				data = envelope["data"]
			}
			must(t, json.Unmarshal(data, &rows))
			actual := []map[string]any{}
			for _, row := range rows {
				selected := map[string]any{}
				for key, val := range row {
					lower := strings.ToLower(key)
					for name := range tc.expect[0] {
						if lower == name {
							selected[lower] = val
						}
					}
				}
				actual = append(actual, selected)
			}
			if !reflect.DeepEqual(actual, tc.expect) {
				t.Fatalf("cube=%s selected=%s expected=%s", raw, pretty(actual), pretty(tc.expect))
			}
		})
	}
}

func runCubeRuntime(t *testing.T, db *sql.DB, subject, mode string, internal bool) *druntime.Runtime {
	resources := resource.New()
	must(t, resources.Register(cube.ReaderDatlyResourceNamespace, cube.ReaderDatlyResources))
	base := payloadArtifact(t, resources, reflect.TypeFor[cube.ReaderComponent](), reflect.TypeFor[cube.RunReportInput](), reflect.TypeFor[cube.RunReportOutput]())
	compiled, err := report.NewProjectCompiler(report.ProjectConfig{}).CompileArtifacts(linkedCubeArtifactInputs[cube.ReaderCubeComponent, cube.ReaderCubeInput, cube.ReaderCubeOutput](t, resources, base, reflect.TypeFor[cube.RunReportInput](), reflect.TypeFor[cube.RunReportOutput](), cube.NewReaderCube))
	must(t, err)
	providers := []locator.Provider{provider.Named("visibility", func(context.Context, reflect.Type, string) (any, bool, error) { return &subject, true, nil }), ordinaryAccess("runaccess", func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
		if name == "reportMode" {
			return mode, true, nil
		}
		return internal, true, nil
	})}
	registered, err := compiled.RuntimeComponents(context.Background(), report.RuntimeConfigureFunc(func(_ context.Context, artifact *report.ComponentArtifact) (report.RuntimeCapabilities, error) {
		if artifact.IsReport() || artifact.ReaderCompilation() == nil {
			return report.RuntimeCapabilities{Providers: providers}, nil
		}
		reader, err := artifact.ReaderCompilation().NewExecution(bootstrap.ReaderRuntimeConfig{SQL: &dsql.SQLComponent{DB: db}})
		return report.RuntimeCapabilities{Reader: reader, Providers: providers}, err
	}))
	must(t, err)
	rt, err := druntime.NewRuntime(registered, druntime.WithResources(resources))
	must(t, err)
	return rt
}

func TestRunCubeLegacySchedulerTotal(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type input struct {
		subject string
		filters map[string]any
	}
	type useCase struct {
		desc   string
		input  input
		expect int
	}
	for _, tc := range []useCase{
		{"anonymous public total", input{}, 2},
		{"owner private and public total", input{subject: "u1"}, 3},
		{"other owner has isolated total", input{subject: "u2"}, 3},
		{"schedule equality total", input{subject: "u1", filters: map[string]any{"scheduleId": "own"}}, 1},
		{"status wildcard total", input{subject: "u1", filters: map[string]any{"status": "RUN%"}}, 2},
		{"error wildcard total", input{subject: "u1", filters: map[string]any{"errorMessage": "%timeout%"}}, 1},
		{"missing schedule has zero total", input{subject: "u1", filters: map[string]any{"scheduleId": "missing"}}, 0},
		{"internal schedules do not contribute", input{subject: "u1", filters: map[string]any{"scheduleId": "internal"}}, 0},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			db, _ := schedulerRunListFixture(t, project)
			filters := map[string]any{}
			for name, value := range tc.input.filters {
				target := map[string]string{"scheduleId": "ScheduleId", "status": "StatusPattern", "errorMessage": "ErrorPattern", "conversationId": "ConversationPattern"}[name]
				filters[target] = value
			}
			body, err := json.Marshal(map[string]any{"measures": map[string]bool{"RecordCount": true}, "filters": filters})
			must(t, err)
			rt := runCubeRuntime(t, db, tc.input.subject, "scheduler", true)
			request := httptest.NewRequest("POST", "/v1/internal/agently/run/report/cube", strings.NewReader(string(body)))
			request.Header.Set("Content-Type", "application/json")
			scope, err := requestprovider.New(request)
			must(t, err)
			defer scope.Close()
			value, err := rt.ExecuteRoute(context.Background(), "POST", "/v1/internal/agently/run/report/cube", scope)
			must(t, err)
			out := value.(*cube.RunReportOutput)
			if len(out.Data) != 1 {
				t.Fatalf("aggregate row count=%d", len(out.Data))
			}
			count := out.Data[0].RecordCount
			if count != tc.expect {
				t.Fatalf("cube=%d expected=%d", count, tc.expect)
			}
		})
	}
}

func TestRunCubeHostAuthorizationCannotBeOverridden(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type input struct {
		subject  string
		internal bool
		filters  map[string]any
	}
	type useCase struct {
		desc   string
		input  input
		expect int
	}
	for _, tc := range []useCase{
		{"subject filter cannot expose another owner's private schedule", input{"", true, map[string]any{"VisibilitySubject": "u1"}}, 2},
		{"report mode filter cannot bypass schedule visibility", input{"", true, map[string]any{"ReportMode": "runs"}}, 2},
		{"internal filter cannot grant trusted scheduler scope", input{"", false, map[string]any{"InternalMode": true}}, -1},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			db, _ := schedulerRunListFixture(t, project)
			rt := runCubeRuntime(t, db, tc.input.subject, "scheduler", tc.input.internal)
			body, err := json.Marshal(map[string]any{"measures": map[string]bool{"RecordCount": true}, "filters": tc.input.filters})
			must(t, err)
			request := httptest.NewRequest("POST", "/v1/internal/agently/run/report/cube", bytes.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			scope, err := requestprovider.New(request)
			must(t, err)
			defer scope.Close()
			value, err := rt.ExecuteRoute(context.Background(), "POST", "/v1/internal/agently/run/report/cube", scope)
			if tc.expect < 0 {
				if err == nil {
					t.Fatal("untrusted scheduler scope was accepted")
				}
				return
			}
			must(t, err)
			out := value.(*cube.RunReportOutput)
			if len(out.Data) != 1 || out.Data[0].RecordCount != tc.expect {
				t.Fatalf("host authorization was not preserved: expected count %d", tc.expect)
			}
		})
	}
}
