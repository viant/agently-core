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

	cube "github.com/viant/agently-core/internal/datly/turn/cube"
	"github.com/viant/bindly/locator"
	requestprovider "github.com/viant/bindly/provider/request"
	"github.com/viant/bindly/resource"
	"github.com/viant/datly/bootstrap"
	"github.com/viant/datly/report"
	druntime "github.com/viant/datly/runtime"
	dsql "github.com/viant/datly/sql"
)

func TestTurnCubeLegacyCounts(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	legacy := legacyProbeBinary(t, project)
	type input struct{ conversation, mode, measure string }
	type useCase struct {
		desc   string
		input  input
		expect int
	}
	for _, tc := range []useCase{
		{"queued conversation count", input{"c1", "queuedCount", "QueuedCount"}, 3},
		{"queued other conversation count", input{"c2", "queuedCount", "QueuedCount"}, 0},
		{"queued empty conversation count", input{"c3", "queuedCount", "QueuedCount"}, 0},
		{"queued blank supplied scope", input{"", "queuedCount", "QueuedCount"}, 0},
		{"controller conversation count", input{"c1", "controllerCount", "ControllerCount"}, 2},
		{"controller other conversation count", input{"c2", "controllerCount", "ControllerCount"}, 1},
		{"controller empty conversation count", input{"c3", "controllerCount", "ControllerCount"}, 0},
		{"controller blank supplied scope", input{"", "controllerCount", "ControllerCount"}, 0},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			_, oldPath := turnCubeFixture(t, project)
			db, _ := turnCubeFixture(t, project)
			payload, err := json.Marshal(map[string]any{"Component": "turnReader", "DBPath": oldPath, "Filters": map[string]any{"mode": tc.input.mode, "conversationId": tc.input.conversation}})
			must(t, err)
			process := exec.Command(legacy)
			process.Stdin = bytes.NewReader(payload)
			raw, err := process.Output()
			must(t, err)
			var before probeResult
			must(t, json.Unmarshal(raw, &before))
			if before.Failed || len(before.Rows) != 1 {
				t.Fatalf("legacy count: %s", raw)
			}
			var old map[string]any
			must(t, json.Unmarshal(before.Rows[0], &old))
			oldCount := int(old[tc.input.measure].(float64))
			rt := turnCubeRuntime(t, db, true, true)
			body := map[string]any{"measures": map[string]bool{tc.input.measure: true}, "filters": map[string]any{"ConversationId": tc.input.conversation}}
			out, err := executeTurnCube(t, rt, body)
			must(t, err)
			if len(out.Data) != 1 {
				t.Fatalf("aggregate rows=%d", len(out.Data))
			}
			count := out.Data[0].QueuedCount
			if tc.input.measure == "ControllerCount" {
				count = out.Data[0].ControllerCount
			}
			if oldCount != tc.expect || count != tc.expect {
				t.Fatalf("count legacy=%d cube=%d expected=%d", oldCount, count, tc.expect)
			}
		})
	}
}

func TestTurnFactCube(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type input struct {
		body              map[string]any
		trusted, provided bool
	}
	type expect struct {
		failed bool
		rows   []map[string]any
	}
	type useCase struct {
		desc   string
		input  input
		expect expect
	}
	measures := map[string]bool{"RecordCount": true, "QueuedCount": true, "ControllerCount": true}
	for _, tc := range []useCase{
		{"one fact cube exposes three measures", input{map[string]any{"measures": measures}, true, true}, expect{rows: []map[string]any{{"RecordCount": float64(6), "QueuedCount": float64(3), "ControllerCount": float64(3)}}}},
		{"empty aggregation returns zero measures", input{map[string]any{"measures": measures, "filters": map[string]any{"ConversationId": "missing"}}, true, true}, expect{rows: []map[string]any{{"RecordCount": float64(0), "QueuedCount": float64(0), "ControllerCount": float64(0)}}}},
		{"status grouping retains conditional measures", input{map[string]any{"measures": measures, "dimensions": map[string]bool{"Status": true}, "orderBy": []string{"status"}}, true, true}, expect{rows: []map[string]any{{"Status": "queued", "RecordCount": float64(3), "QueuedCount": float64(3), "ControllerCount": float64(1)}, {"Status": "running", "RecordCount": float64(2), "QueuedCount": float64(0), "ControllerCount": float64(2)}, {"Status": "waiting_for_user", "RecordCount": float64(1), "QueuedCount": float64(0), "ControllerCount": float64(0)}}}},

		{"nullable origin remains a distinct grouping dimension", input{map[string]any{"measures": map[string]bool{"RecordCount": true}, "dimensions": map[string]bool{"Origin": true}, "orderBy": []string{"origin"}}, true, true}, expect{rows: []map[string]any{{"Origin": nil, "RecordCount": float64(3)}, {"Origin": "controller", "RecordCount": float64(3)}}}},
		{"declared status and origin filters compose", input{map[string]any{"measures": measures, "filters": map[string]any{"Statuses": []string{"queued"}, "Origin": "controller"}}, true, true}, expect{rows: []map[string]any{{"RecordCount": float64(1), "QueuedCount": float64(1), "ControllerCount": float64(1)}}}},
		{"missing trusted host binding fails", input{map[string]any{"measures": measures}, false, false}, expect{failed: true}},
		{"request filter cannot grant internal permission", input{map[string]any{"measures": measures, "filters": map[string]any{"Trusted": true}}, false, true}, expect{failed: true}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			db, _ := turnCubeFixture(t, project)
			rt := turnCubeRuntime(t, db, tc.input.trusted, tc.input.provided)
			out, err := executeTurnCube(t, rt, tc.input.body)
			if (err != nil) != tc.expect.failed {
				t.Fatalf("failure=%v expected=%v", err, tc.expect.failed)
			}
			if err != nil {
				return
			}
			raw, err := json.Marshal(out.Data)
			must(t, err)
			var rows []map[string]any
			must(t, json.Unmarshal(raw, &rows))
			selected := []map[string]any{}
			for _, row := range rows {
				item := map[string]any{}
				for key := range tc.expect.rows[0] {
					item[key] = row[key]
				}
				selected = append(selected, item)
			}
			if !reflect.DeepEqual(selected, tc.expect.rows) {
				t.Fatalf("rows=%s expected=%s", pretty(selected), pretty(tc.expect.rows))
			}
		})
	}
}
func executeTurnCube(t *testing.T, rt *druntime.Runtime, body map[string]any) (*cube.TurnReportOutput, error) {
	raw, err := json.Marshal(body)
	must(t, err)
	request := httptest.NewRequest("POST", "/v1/internal/agently/turn/report/cube", strings.NewReader(string(raw)))
	request.Header.Set("Content-Type", "application/json")
	scope, err := requestprovider.New(request)
	must(t, err)
	defer scope.Close()
	value, err := rt.ExecuteRoute(context.Background(), "POST", "/v1/internal/agently/turn/report/cube", scope)
	if err != nil {
		return nil, err
	}
	return value.(*cube.TurnReportOutput), nil
}
func turnCubeFixture(t *testing.T, project string) (*sql.DB, string) {
	db, path := turnReaderFixture(t, project)
	_, err := db.Exec(`UPDATE turn SET origin='controller' WHERE id IN ('a','f','e')`)
	must(t, err)
	return db, path
}
func turnCubeRuntime(t *testing.T, db *sql.DB, trusted, provided bool) *druntime.Runtime {
	resources := resource.New()
	must(t, resources.Register(cube.ReaderDatlyResourceNamespace, cube.ReaderDatlyResources))
	base := payloadArtifact(t, resources, reflect.TypeFor[cube.ReaderComponent](), reflect.TypeFor[cube.TurnReportInput](), reflect.TypeFor[cube.TurnReportOutput]())
	compiled, err := report.NewProjectCompiler(report.ProjectConfig{}).CompileArtifacts([]bootstrap.ArtifactInput{{Component: base.Component, InputType: reflect.TypeFor[cube.TurnReportInput](), OutputType: reflect.TypeFor[cube.TurnReportOutput](), Resources: resources}})
	must(t, err)
	providers := []locator.Provider{}
	if provided {
		providers = append(providers, ordinaryAccess("turnaccess", func(context.Context, reflect.Type, string) (any, bool, error) { return trusted, true, nil }))
	}
	registered, err := compiled.RuntimeComponents(context.Background(), report.RuntimeConfigureFunc(func(_ context.Context, artifact *report.ComponentArtifact) (report.RuntimeCapabilities, error) {
		if artifact.IsReport() {
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
