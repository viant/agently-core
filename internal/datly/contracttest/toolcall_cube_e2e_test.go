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
	"testing"

	cube "github.com/viant/agently-core/internal/datly/toolcall/cube"
	"github.com/viant/bindly/locator"
	requestprovider "github.com/viant/bindly/provider/request"
	"github.com/viant/bindly/resource"
	"github.com/viant/datly/bootstrap"
	"github.com/viant/datly/report"
	druntime "github.com/viant/datly/runtime"
	provider "github.com/viant/datly/runtime/handler/provider"
	dsql "github.com/viant/datly/sql"
)

func TestToolCallCubeLegacyCounts(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	legacy := legacyProbeBinary(t, project)
	type input struct{ mode, conversation, turn, op string }
	type useCase struct {
		desc   string
		input  input
		expect int
	}
	for _, tc := range []useCase{
		{"operation in first conversation", input{"byOp", "c1", "", "shared"}, 3},
		{"operation across trusted scope", input{"byOp", "", "", "shared"}, 4},
		{"operation in second conversation", input{"byOp", "c2", "", "shared"}, 1},
		{"missing operation", input{"byOp", "c1", "", "missing"}, 0},
		{"consistent first turn", input{"byTurn", "c1", "t1", ""}, 3},
		{"consistent second turn", input{"byTurn", "c2", "t2", ""}, 1},
		{"wrong conversation turn", input{"byTurn", "c2", "t1", ""}, 0},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			_, oldPath := toolCallCubeFixture(t, project)
			db, _ := toolCallCubeFixture(t, project)
			oldFilters := map[string]any{"mode": tc.input.mode}
			filters := map[string]any{}
			if tc.input.conversation != "" {
				oldFilters["conversationId"] = tc.input.conversation
				filters["ConversationId"] = tc.input.conversation
			}
			if tc.input.op != "" {
				oldFilters["opId"] = tc.input.op
				filters["OpId"] = tc.input.op
			}
			if tc.input.turn != "" {
				oldFilters["turnId"] = tc.input.turn
				filters["TurnId"] = tc.input.turn
				filters["ConsistentTurn"] = true
			}
			raw, err := json.Marshal(map[string]any{"Component": "toolCallReader", "DBPath": oldPath, "Filters": oldFilters})
			must(t, err)
			process := exec.Command(legacy)
			process.Stdin = bytes.NewReader(raw)
			raw, err = process.Output()
			must(t, err)
			var before probeResult
			must(t, json.Unmarshal(raw, &before))
			if before.Failed {
				t.Fatalf("legacy: %s", before.Error)
			}
			rt := toolCallCubeRuntime(t, db, "", true, true)
			out, err := executeToolCallCube(t, rt, map[string]any{"measures": map[string]bool{"RecordCount": true}, "filters": filters})
			must(t, err)
			if len(before.Rows) != tc.expect || len(out.Data) != 1 || out.Data[0].RecordCount != tc.expect {
				t.Fatalf("legacy count=%d cube=%s expected=%d", len(before.Rows), pretty(out.Data), tc.expect)
			}
		})
	}
}
func TestToolCallFactCube(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type input struct {
		subject            string
		internal, provided bool
		filters            map[string]any
	}
	type expect struct {
		failed         bool
		count, retries int
		latency        *int
		average, cost  *float64
	}
	type useCase struct {
		desc   string
		input  input
		expect expect
	}
	integer := func(v int) *int { return &v }
	number := func(v float64) *float64 { return &v }
	for _, tc := range []useCase{
		{"all facts preserve unknown cost", input{"", true, true, nil}, expect{count: 6, retries: 2, latency: integer(540), average: number(180)}},
		{"anonymous sees public", input{"", false, true, nil}, expect{count: 1, latency: integer(270), average: number(270)}},
		{"owner sees own and public", input{"u1", false, true, nil}, expect{count: 5, retries: 2, latency: integer(540), average: number(180)}},
		{"other owner isolated", input{"u2", false, true, nil}, expect{count: 2, latency: integer(270), average: number(270)}},
		{"tool name filter", input{"", true, true, map[string]any{"ToolName": "different"}}, expect{count: 1, cost: number(0)}},
		{"tool kind filter", input{"", true, true, map[string]any{"ToolKind": "builtin"}}, expect{count: 1, latency: integer(270), average: number(270)}},
		{"status filter", input{"", true, true, map[string]any{"Statuses": []string{"running"}}}, expect{count: 2, retries: 1, latency: integer(90), average: number(90)}},
		{"missing facts keep empty measures nullable", input{"", true, true, map[string]any{"ConversationId": "missing"}}, expect{}},
		{"request cannot replace host scope", input{"", false, true, map[string]any{"Internal": true, "VisibilitySubject": "u1"}}, expect{count: 1, latency: integer(270), average: number(270)}},
		{"missing required host fails", input{"", false, false, nil}, expect{failed: true}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			db, _ := toolCallCubeFixture(t, project)
			rt := toolCallCubeRuntime(t, db, tc.input.subject, tc.input.internal, tc.input.provided)
			out, err := executeToolCallCube(t, rt, map[string]any{"measures": map[string]bool{"RecordCount": true, "RetryCount": true, "TotalLatencyMs": true, "AverageLatencyMs": true, "Cost": true}, "filters": tc.input.filters})
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
			if row.RecordCount != tc.expect.count || row.RetryCount != tc.expect.retries || !reflect.DeepEqual(row.TotalLatencyMs, tc.expect.latency) || !reflect.DeepEqual(row.AverageLatencyMs, tc.expect.average) || !reflect.DeepEqual(row.Cost, tc.expect.cost) {
				t.Fatalf("aggregate=%s expected=%s", pretty(row), pretty(tc.expect))
			}
		})
	}
}
func toolCallCubeFixture(t *testing.T, project string) (*sql.DB, string) {
	db, path := toolCallReaderFixture(t, project)
	_, err := db.Exec(`UPDATE tool_call SET latency_ms=90 WHERE message_id='existing';UPDATE tool_call SET latency_ms=180,cost=2.5 WHERE message_id='new';UPDATE tool_call SET latency_ms=270 WHERE message_id='public';UPDATE tool_call SET cost=0 WHERE message_id='independent'`)
	must(t, err)
	return db, path
}
func executeToolCallCube(t *testing.T, rt *druntime.Runtime, body map[string]any) (*cube.ToolCallReportOutput, error) {
	raw, err := json.Marshal(body)
	must(t, err)
	request := httptest.NewRequest("POST", "/v1/internal/agently/tool-call/report/cube", bytes.NewReader(raw))
	request.Header.Set("Content-Type", "application/json")
	scope, err := requestprovider.New(request)
	must(t, err)
	defer scope.Close()
	value, err := rt.ExecuteRoute(context.Background(), "POST", "/v1/internal/agently/tool-call/report/cube", scope)
	if err != nil {
		return nil, err
	}
	return value.(*cube.ToolCallReportOutput), nil
}
func toolCallCubeRuntime(t *testing.T, db *sql.DB, subject string, internal, provided bool) *druntime.Runtime {
	resources := resource.New()
	must(t, resources.Register(cube.ReaderDatlyResourceNamespace, cube.ReaderDatlyResources))
	base := payloadArtifact(t, resources, reflect.TypeFor[cube.ReaderComponent](), reflect.TypeFor[cube.ToolCallReportInput](), reflect.TypeFor[cube.ToolCallReportOutput]())
	compiled, err := report.NewProjectCompiler(report.ProjectConfig{}).CompileArtifacts([]bootstrap.ArtifactInput{{Component: base.Component, InputType: reflect.TypeFor[cube.ToolCallReportInput](), OutputType: reflect.TypeFor[cube.ToolCallReportOutput](), Resources: resources}})
	must(t, err)
	providers := []locator.Provider{}
	if provided {
		providers = append(providers, ordinaryAccess("toolcallaccess", func(context.Context, reflect.Type, string) (any, bool, error) { return internal, true, nil }), provider.Named("visibility", func(context.Context, reflect.Type, string) (any, bool, error) { return &subject, true, nil }))
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

func TestToolCallCubeGroupingAndKnownCost(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type input struct {
		known bool
		body  map[string]any
	}
	type useCase struct {
		desc   string
		input  input
		expect []map[string]any
	}
	for _, tc := range []useCase{
		{"status grouping preserves conditional retries and unknown cost", input{false, map[string]any{"dimensions": map[string]bool{"Status": true}, "measures": map[string]bool{"RecordCount": true, "RetryCount": true, "Cost": true}, "orderBy": []string{"status"}}}, []map[string]any{
			{"status": "completed", "recordcount": float64(2), "retrycount": float64(1), "cost": nil},
			{"status": "failed", "recordcount": float64(1), "retrycount": float64(0), "cost": nil},
			{"status": "pending", "recordcount": float64(1), "retrycount": float64(0), "cost": float64(0)},
			{"status": "running", "recordcount": float64(2), "retrycount": float64(1), "cost": nil},
		}},
		{"all known cost values preserve total", input{true, map[string]any{"measures": map[string]bool{"RecordCount": true, "Cost": true}}}, []map[string]any{{"recordcount": float64(6), "cost": float64(4)}}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			db, _ := toolCallCubeFixture(t, project)
			if tc.input.known {
				_, err := db.Exec("UPDATE tool_call SET cost=0 WHERE cost IS NULL")
				must(t, err)
			}
			rt := toolCallCubeRuntime(t, db, "", true, true)
			out, err := executeToolCallCube(t, rt, tc.input.body)
			must(t, err)
			raw, err := json.Marshal(out.Data)
			must(t, err)
			var rows []json.RawMessage
			must(t, json.Unmarshal(raw, &rows))
			normalized := normalizeRowsInOrder(t, rows)
			selected := []map[string]any{}
			for _, row := range normalized {
				item := map[string]any{}
				for key := range tc.expect[0] {
					item[key] = row[key]
				}
				selected = append(selected, item)
			}
			if !reflect.DeepEqual(selected, tc.expect) {
				t.Fatalf("grouped=%s expected=%s", pretty(selected), pretty(tc.expect))
			}
		})
	}
}
