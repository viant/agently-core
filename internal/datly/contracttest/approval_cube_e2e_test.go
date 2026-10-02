package tests

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	cube "github.com/viant/agently-core/internal/datly/toolapprovalqueue/cube"
	"github.com/viant/bindly/locator"
	requestprovider "github.com/viant/bindly/provider/request"
	"github.com/viant/bindly/resource"
	"github.com/viant/datly/bootstrap"
	"github.com/viant/datly/report"
	druntime "github.com/viant/datly/runtime"
	provider "github.com/viant/datly/runtime/handler/provider"
	dsql "github.com/viant/datly/sql"
)

func TestApprovalCubeLegacyCounts(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type input struct {
		mode, measure string
		filters       map[string]any
	}
	type useCase struct {
		desc   string
		input  input
		expect int
	}
	for _, tc := range []useCase{
		{"total fact count", input{"count", "TotalCount", nil}, 7},
		{"owner count", input{"count", "TotalCount", map[string]any{"userId": "u1"}}, 6},
		{"conversation count", input{"count", "TotalCount", map[string]any{"conversationId": "c1"}}, 6},
		{"tool count", input{"count", "TotalCount", map[string]any{"toolName": "shell/exec"}}, 5},
		{"status count", input{"count", "TotalCount", map[string]any{"status": "pending"}}, 1},
		{"blank identity count", input{"count", "TotalCount", map[string]any{"id": ""}}, 0},
		{"conversation pending count", input{"pendingCount", "PendingCount", map[string]any{"conversationId": "c1"}}, 1},
		{"other pending count", input{"pendingCount", "PendingCount", map[string]any{"conversationId": "c2"}}, 0},
		{"empty pending conversation", input{"pendingCount", "PendingCount", map[string]any{"conversationId": "c3"}}, 0},
		{"blank supplied pending scope", input{"pendingCount", "PendingCount", map[string]any{"conversationId": ""}}, 0},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			db, _ := approvalReaderFixture(t, project)
			nativeFilters := map[string]any{}
			names := map[string]string{"userId": "UserId", "conversationId": "ConversationId", "toolName": "ToolName", "status": "QueueStatus", "id": "Id"}
			for k, v := range tc.input.filters {
				nativeFilters[names[k]] = v
			}
			out, err := executeApprovalCube(t, approvalCubeRuntime(t, db, "", true, true), map[string]any{"measures": map[string]bool{tc.input.measure: true}, "filters": nativeFilters})
			must(t, err)
			if len(out.Data) != 1 {
				t.Fatal("missing aggregate")
			}
			count := out.Data[0].TotalCount
			if tc.input.measure == "PendingCount" {
				count = out.Data[0].PendingCount
			}
			if count != tc.expect {
				t.Fatalf("native=%d expected=%d", count, tc.expect)
			}
		})
	}
}
func TestApprovalCubeScopeAndDimensions(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type input struct {
		subject            string
		internal, provided bool
		filters            map[string]any
	}
	type expect struct {
		failed                             bool
		total, pending, outcomes, timedOut int
	}
	type useCase struct {
		desc   string
		input  input
		expect expect
	}
	measures := map[string]bool{"TotalCount": true, "PendingCount": true, "OutcomeCount": true, "TimedOutCount": true}
	for _, tc := range []useCase{
		{"four native fact measures", input{"", true, true, nil}, expect{total: 7, pending: 1, outcomes: 6, timedOut: 1}},
		{"anonymous has no personal facts", input{"", false, true, nil}, expect{}},
		{"owner scoped facts", input{"u1", false, true, nil}, expect{total: 6, pending: 1, outcomes: 5, timedOut: 1}},
		{"other owner facts", input{"u2", false, true, nil}, expect{total: 1, outcomes: 1}},
		{"filter cannot replace host owner", input{"u1", false, true, map[string]any{"UserId": "u2"}}, expect{}},
		{"status filter scopes every measure", input{"", true, true, map[string]any{"QueueStatus": "approved"}}, expect{total: 2, outcomes: 2}},
		{"missing host scope fails", input{"", false, false, nil}, expect{failed: true}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			db, _ := approvalReaderFixture(t, project)
			out, err := executeApprovalCube(t, approvalCubeRuntime(t, db, tc.input.subject, tc.input.internal, tc.input.provided), map[string]any{"measures": measures, "filters": tc.input.filters})
			if (err != nil) != tc.expect.failed {
				t.Fatalf("failure=%v expected=%v", err, tc.expect.failed)
			}
			if err != nil {
				return
			}
			if len(out.Data) != 1 || out.Data[0].TotalCount != tc.expect.total || out.Data[0].PendingCount != tc.expect.pending || out.Data[0].OutcomeCount != tc.expect.outcomes || out.Data[0].TimedOutCount != tc.expect.timedOut {
				t.Fatalf("rows=%s expected=%v", pretty(out.Data), tc.expect)
			}
		})
	}
	t.Run("nullable decision grouping", func(t *testing.T) {
		db, _ := approvalReaderFixture(t, project)
		out, err := executeApprovalCube(t, approvalCubeRuntime(t, db, "", true, true), map[string]any{"dimensions": map[string]bool{"Decision": true}, "measures": map[string]bool{"TotalCount": true}})
		must(t, err)
		var count int
		hasNull := false
		for _, row := range out.Data {
			count += row.TotalCount
			hasNull = hasNull || row.Decision == nil
		}
		if count != 7 || !hasNull {
			t.Fatalf("rows=%s", pretty(out.Data))
		}
	})
}
func executeApprovalCube(t *testing.T, rt *druntime.Runtime, body map[string]any) (*cube.ApprovalReportOutput, error) {
	raw, err := json.Marshal(body)
	must(t, err)
	request := httptest.NewRequest("POST", "/v1/internal/agently/tool-approval/report/cube", bytes.NewReader(raw))
	request.Header.Set("Content-Type", "application/json")
	scope, err := requestprovider.New(request)
	must(t, err)
	defer scope.Close()
	value, err := rt.ExecuteRoute(context.Background(), "POST", "/v1/internal/agently/tool-approval/report/cube", scope)
	if err != nil {
		return nil, err
	}
	return value.(*cube.ApprovalReportOutput), nil
}
func approvalCubeRuntime(t *testing.T, db *sql.DB, subject string, internal, provided bool) *druntime.Runtime {
	resources := resource.New()
	must(t, resources.Register(cube.ReaderDatlyResourceNamespace, cube.ReaderDatlyResources))
	base := payloadArtifact(t, resources, reflect.TypeFor[cube.ReaderComponent](), reflect.TypeFor[cube.ApprovalReportInput](), reflect.TypeFor[cube.ApprovalReportOutput]())
	compiled, err := report.NewProjectCompiler(report.ProjectConfig{}).CompileArtifacts([]bootstrap.ArtifactInput{{Component: base.Component, InputType: reflect.TypeFor[cube.ApprovalReportInput](), OutputType: reflect.TypeFor[cube.ApprovalReportOutput](), Resources: resources}})
	must(t, err)
	providers := []locator.Provider{}
	if provided {
		providers = append(providers, ordinaryAccess("approvalaccess", func(context.Context, reflect.Type, string) (any, bool, error) { return internal, true, nil }), provider.Named("visibility", func(context.Context, reflect.Type, string) (any, bool, error) { return &subject, true, nil }))
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
