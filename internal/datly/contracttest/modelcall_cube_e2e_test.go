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

	cube "github.com/viant/agently-core/internal/datly/modelcall/cube"
	"github.com/viant/bindly/locator"
	requestprovider "github.com/viant/bindly/provider/request"
	"github.com/viant/bindly/resource"
	"github.com/viant/datly/bootstrap"
	"github.com/viant/datly/report"
	druntime "github.com/viant/datly/runtime"
	provider "github.com/viant/datly/runtime/handler/provider"
	dsql "github.com/viant/datly/sql"
)

var modelCallUsageMeasures = map[string]bool{"Cost": true, "PromptTokens": true, "PromptCachedTokens": true, "PromptAudioTokens": true, "CompletionTokens": true, "CompletionReasoningTokens": true, "CompletionAudioTokens": true, "CompletionAcceptedPredictionTokens": true, "CompletionRejectedPredictionTokens": true, "TotalTokens": true}

func TestModelCallCubeLegacyUsage(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	legacy := legacyProbeBinary(t, project)
	type input struct {
		conversation       string
		models, knownCosts bool
	}
	type useCase struct {
		desc   string
		input  input
		expect int
	}
	for _, tc := range []useCase{
		{"conversation usage retains unknown aggregate cost", input{"c1", false, false}, 1},
		{"conversation known costs sum", input{"c1", false, true}, 1},
		{"other conversation usage", input{"c2", false, false}, 1},
		{"all-null usage token sums are zero", input{"c3", false, false}, 1},
		{"missing conversation has no grouped usage", input{"missing", false, false}, 0},
		{"model role groups preserve router sidecar and worker", input{"c1", true, false}, 3},
		{"known model role costs preserve zero", input{"c1", true, true}, 3},
		{"fallback react role", input{"c2", true, false}, 1},
		{"explicit narrator role is case insensitive", input{"c3", true, false}, 1},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			_, oldPath := modelCallCubeFixture(t, project, tc.input.knownCosts)
			db, _ := modelCallCubeFixture(t, project, tc.input.knownCosts)
			payload, err := json.Marshal(map[string]any{"Component": "modelCallUsage", "DBPath": oldPath, "Filters": map[string]any{"id": tc.input.conversation, "models": tc.input.models}})
			must(t, err)
			process := exec.Command(legacy)
			process.Stdin = bytes.NewReader(payload)
			raw, err := process.Output()
			must(t, err)
			var before probeResult
			must(t, json.Unmarshal(raw, &before))
			if before.Failed {
				t.Fatalf("legacy usage: %s", before.Error)
			}
			dimensions := map[string]bool{"ConversationId": true}
			if tc.input.models {
				dimensions["Provider"] = true
				dimensions["Model"] = true
				dimensions["ExecutionRole"] = true
			}
			rt := modelCallCubeRuntime(t, db, "", true, true)
			out, err := executeModelCallCube(t, rt, map[string]any{"dimensions": dimensions, "measures": modelCallUsageMeasures, "filters": map[string]any{"ConversationId": tc.input.conversation}})
			must(t, err)
			raw, err = json.Marshal(out.Data)
			must(t, err)
			var rows []json.RawMessage
			must(t, json.Unmarshal(raw, &rows))
			fields := []string{"conversationid"}
			if tc.input.models {
				fields = append(fields, "provider", "model", "executionrole")
			}
			for name := range modelCallUsageMeasures {
				fields = append(fields, strings.ToLower(name))
			}
			oldRows, newRows := modelCallUsageRows(t, before.Rows, fields), modelCallUsageRows(t, rows, fields)
			if len(newRows) != tc.expect || !reflect.DeepEqual(oldRows, newRows) {
				t.Fatalf("usage legacy=%s cube=%s expectedRows=%d", pretty(oldRows), pretty(newRows), tc.expect)
			}
		})
	}
}

func TestModelCallFactCubeScope(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type input struct {
		subject            string
		internal, provided bool
		knownCosts         bool
		filters            map[string]any
	}
	type expect struct {
		failed        bool
		empty         bool
		count         int
		prompt, total int
		cost          *float64
	}
	type useCase struct {
		desc   string
		input  input
		expect expect
	}
	knownCost := 4.0
	for _, tc := range []useCase{
		{"trusted all-fact scope keeps unknown cost", input{"", true, true, false, nil}, expect{count: 5, prompt: 15, total: 23}},
		{"anonymous sees only public facts", input{"", false, true, false, nil}, expect{count: 1}},
		{"owner sees own and public facts", input{"u1", false, true, false, nil}, expect{count: 4, prompt: 15, total: 23}},
		{"other owner has isolated scope", input{"u2", false, true, false, nil}, expect{count: 2}},
		{"declared provider filter", input{"", true, true, false, map[string]any{"Provider": "openai"}}, expect{count: 1, prompt: 5, total: 8, cost: func() *float64 { v := 2.5; return &v }()}},
		{"declared execution role filter", input{"", true, true, false, map[string]any{"ExecutionRole": "intake"}}, expect{count: 1, prompt: 5, total: 8, cost: func() *float64 { v := 2.5; return &v }()}},
		{"request cannot replace host authorization", input{"", false, true, false, map[string]any{"Internal": true, "VisibilitySubject": "u1"}}, expect{count: 1}},
		{"all known costs preserve total", input{"", true, true, true, map[string]any{"ConversationId": "c1"}}, expect{count: 3, prompt: 15, total: 23, cost: &knownCost}},

		{"declared model filter", input{"", true, true, false, map[string]any{"Model": "gpt"}}, expect{count: 1, prompt: 5, total: 8, cost: func() *float64 { v := 2.5; return &v }()}},
		{"declared status filter", input{"", true, true, false, map[string]any{"Statuses": []string{"thinking"}}}, expect{count: 1, prompt: 10, total: 15, cost: func() *float64 { v := 1.5; return &v }()}},
		{"empty aggregate has zero count and nullable sums", input{"", true, true, false, map[string]any{"ConversationId": "missing"}}, expect{empty: true}},
		{"missing required host scope fails", input{"", false, false, false, nil}, expect{failed: true}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			db, _ := modelCallCubeFixture(t, project, tc.input.knownCosts)
			rt := modelCallCubeRuntime(t, db, tc.input.subject, tc.input.internal, tc.input.provided)
			out, err := executeModelCallCube(t, rt, map[string]any{"measures": map[string]bool{"RecordCount": true, "PromptTokens": true, "TotalTokens": true, "Cost": true}, "filters": tc.input.filters})
			if (err != nil) != tc.expect.failed {
				t.Fatalf("failure=%v expected=%v", err, tc.expect.failed)
			}
			if err != nil {
				return
			}
			if len(out.Data) != 1 {
				t.Fatalf("aggregate rows=%d", len(out.Data))
			}
			row := out.Data[0]
			if tc.expect.empty {
				if row.RecordCount != 0 || row.Cost != nil || row.PromptTokens != nil || row.TotalTokens != nil {
					t.Fatalf("empty aggregate=%s", pretty(row))
				}
				return
			}
			if row.RecordCount != tc.expect.count || row.PromptTokens == nil || *row.PromptTokens != tc.expect.prompt || row.TotalTokens == nil || *row.TotalTokens != tc.expect.total || !reflect.DeepEqual(row.Cost, tc.expect.cost) {
				t.Fatalf("aggregate=%s expected=%s", pretty(row), pretty(tc.expect))
			}
		})
	}
}
func modelCallCubeFixture(t *testing.T, project string, knownCosts bool) (*sql.DB, string) {
	db, path := modelCallReaderFixture(t, project)
	_, err := db.Exec(`UPDATE model_call SET prompt_tokens=5,prompt_cached_tokens=1,prompt_audio_tokens=2,completion_tokens=3,completion_reasoning_tokens=1,completion_audio_tokens=0,completion_accepted_prediction_tokens=2,completion_rejected_prediction_tokens=0,total_tokens=8,cost=2.5 WHERE message_id='new';
 UPDATE message SET mode='ROUTER' WHERE id='new'; UPDATE message SET mode='worker' WHERE id='user-call'; UPDATE message SET mode='Narrator' WHERE id='public'; UPDATE turn SET agent_id_used='planner_pass' WHERE id='t1';`)
	must(t, err)
	if knownCosts {
		_, err = db.Exec("UPDATE model_call SET cost=0 WHERE cost IS NULL")
		must(t, err)
	}
	return db, path
}
func executeModelCallCube(t *testing.T, rt *druntime.Runtime, body map[string]any) (*cube.ModelCallReportOutput, error) {
	raw, err := json.Marshal(body)
	must(t, err)
	request := httptest.NewRequest("POST", "/v1/internal/agently/model-call/report/cube", bytes.NewReader(raw))
	request.Header.Set("Content-Type", "application/json")
	scope, err := requestprovider.New(request)
	must(t, err)
	defer scope.Close()
	value, err := rt.ExecuteRoute(context.Background(), "POST", "/v1/internal/agently/model-call/report/cube", scope)
	if err != nil {
		return nil, err
	}
	return value.(*cube.ModelCallReportOutput), nil
}
func modelCallCubeRuntime(t *testing.T, db *sql.DB, subject string, internal, provided bool) *druntime.Runtime {
	resources := resource.New()
	must(t, resources.Register(cube.ReaderDatlyResourceNamespace, cube.ReaderDatlyResources))
	base := payloadArtifact(t, resources, reflect.TypeFor[cube.ReaderComponent](), reflect.TypeFor[cube.ModelCallReportInput](), reflect.TypeFor[cube.ModelCallReportOutput]())
	compiled, err := report.NewProjectCompiler(report.ProjectConfig{}).CompileArtifacts([]bootstrap.ArtifactInput{{Component: base.Component, InputType: reflect.TypeFor[cube.ModelCallReportInput](), OutputType: reflect.TypeFor[cube.ModelCallReportOutput](), Resources: resources}})
	must(t, err)
	providers := []locator.Provider{}
	if provided {
		providers = append(providers, ordinaryAccess("modelcallaccess", func(context.Context, reflect.Type, string) (any, bool, error) { return internal, true, nil }), provider.Named("visibility", func(context.Context, reflect.Type, string) (any, bool, error) { return &subject, true, nil }))
	}
	registered, err := compiled.RuntimeComponents(context.Background(), report.RuntimeConfigureFunc(func(_ context.Context, artifact *report.ComponentArtifact) (report.RuntimeCapabilities, error) {
		if artifact.IsReport() {
			return report.RuntimeCapabilities{Providers: providers}, nil
		}
		execution, err := artifact.ReaderCompilation().NewExecution(bootstrap.ReaderRuntimeConfig{SQL: &dsql.SQLComponent{DB: db}})
		return report.RuntimeCapabilities{Reader: execution, Providers: providers}, err
	}))
	must(t, err)
	rt, err := druntime.NewRuntime(registered, druntime.WithResources(resources))
	must(t, err)
	return rt
}
func modelCallUsageRows(t *testing.T, rows []json.RawMessage, fields []string) []map[string]any {
	result := normalizeRowsInOrder(t, rows)
	for i, row := range result {
		selected := map[string]any{}
		for _, field := range fields {
			selected[field] = row[field]
		}
		result[i] = selected
	}
	sort.Slice(result, func(i, j int) bool { return pretty(result[i]) < pretty(result[j]) })
	return result
}
