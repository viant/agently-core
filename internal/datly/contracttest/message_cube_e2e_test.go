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

	cube "github.com/viant/agently-core/internal/datly/message/cube"
	"github.com/viant/bindly/locator"
	requestprovider "github.com/viant/bindly/provider/request"
	"github.com/viant/bindly/resource"
	"github.com/viant/datly/bootstrap"
	"github.com/viant/datly/report"
	druntime "github.com/viant/datly/runtime"
	provider "github.com/viant/datly/runtime/handler/provider"
	dsql "github.com/viant/datly/sql"
)

func TestMessageCubeLegacyPendingCount(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	legacy := legacyProbeBinary(t, project)
	type useCase struct {
		desc, input string
		expect      int
	}
	for _, tc := range []useCase{{"pending includes responses with elicitation id", "c1", 2}, {"other conversation", "c2", 0}, {"empty conversation", "c3", 0}, {"blank supplied scope", "", 0}} {
		t.Run(tc.desc, func(t *testing.T) {
			_, path := messageCubeFixture(t, project)
			db, _ := messageCubeFixture(t, project)
			payload, err := json.Marshal(map[string]any{"Component": "messageReader", "DBPath": path, "Filters": map[string]any{"mode": "pendingCount", "conversationId": tc.input}})
			must(t, err)
			process := exec.Command(legacy)
			process.Stdin = bytes.NewReader(payload)
			raw, err := process.Output()
			must(t, err)
			var before probeResult
			must(t, json.Unmarshal(raw, &before))
			if before.Failed || len(before.Rows) != 1 {
				t.Fatalf("legacy: %s", raw)
			}
			var old map[string]any
			must(t, json.Unmarshal(before.Rows[0], &old))
			out, err := executeMessageCube(t, messageCubeRuntime(t, db, "", true, true), map[string]any{"measures": map[string]bool{"PendingCount": true}, "filters": map[string]any{"ConversationId": tc.input}})
			must(t, err)
			if len(out.Data) != 1 || out.Data[0].PendingCount != tc.expect || int(old["PendingCount"].(float64)) != tc.expect {
				t.Fatalf("legacy=%v native=%v expected=%d", old, out.Data, tc.expect)
			}
		})
	}
}
func TestMessageFactCubeScopeAndGrouping(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type input struct {
		subject            string
		internal, provided bool
		body               map[string]any
	}
	type expect struct {
		failed           bool
		records, pending int
	}
	type useCase struct {
		desc   string
		input  input
		expect expect
	}
	measures := map[string]bool{"RecordCount": true, "PendingCount": true, "UserCount": true, "AssistantCount": true}
	for _, tc := range []useCase{
		{"trusted fact totals", input{"", true, true, map[string]any{"measures": measures}}, expect{records: 6, pending: 2}},
		{"anonymous public facts", input{"", false, true, map[string]any{"measures": measures}}, expect{records: 1}},
		{"owner sees own and public facts", input{"u1", false, true, map[string]any{"measures": measures}}, expect{records: 6, pending: 2}},
		{"other identity cannot request private conversation", input{"u2", false, true, map[string]any{"measures": measures, "filters": map[string]any{"ConversationId": "c1"}}}, expect{}},
		{"missing host fails", input{"", false, false, map[string]any{"measures": measures}}, expect{failed: true}},
		{"role predicate scopes all measures", input{"", true, true, map[string]any{"measures": measures, "filters": map[string]any{"Roles": []string{"assistant"}}}}, expect{records: 3, pending: 1}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			db, _ := messageCubeFixture(t, project)
			out, err := executeMessageCube(t, messageCubeRuntime(t, db, tc.input.subject, tc.input.internal, tc.input.provided), tc.input.body)
			if (err != nil) != tc.expect.failed {
				t.Fatalf("error=%v expected=%v", err, tc.expect.failed)
			}
			if err != nil {
				return
			}
			if len(out.Data) != 1 || out.Data[0].RecordCount != tc.expect.records || out.Data[0].PendingCount != tc.expect.pending {
				t.Fatalf("rows=%s expected=%v", pretty(out.Data), tc.expect)
			}
		})
	}
	t.Run("nullable grouped dimension", func(t *testing.T) {
		db, _ := messageCubeFixture(t, project)
		out, err := executeMessageCube(t, messageCubeRuntime(t, db, "", true, true), map[string]any{"dimensions": map[string]bool{"Phase": true}, "measures": map[string]bool{"RecordCount": true}})
		must(t, err)
		var records int
		hasNull := false
		for _, row := range out.Data {
			records += row.RecordCount
			hasNull = hasNull || row.Phase == nil
		}
		if records != 6 || !hasNull {
			t.Fatalf("group rows=%s", pretty(out.Data))
		}
	})
}
func messageCubeFixture(t *testing.T, project string) (*sql.DB, string) {
	db, path := messageReaderFixture(t, project)
	_, err := db.Exec(`UPDATE message SET status='pending' WHERE id IN ('b','d','e'); UPDATE message SET status='completed' WHERE id='c';`)
	must(t, err)
	return db, path
}
func executeMessageCube(t *testing.T, rt *druntime.Runtime, body map[string]any) (*cube.MessageReportOutput, error) {
	raw, err := json.Marshal(body)
	must(t, err)
	request := httptest.NewRequest("POST", "/v1/internal/agently/message/report/cube", bytes.NewReader(raw))
	request.Header.Set("Content-Type", "application/json")
	scope, err := requestprovider.New(request)
	must(t, err)
	defer scope.Close()
	value, err := rt.ExecuteRoute(context.Background(), "POST", "/v1/internal/agently/message/report/cube", scope)
	if err != nil {
		return nil, err
	}
	return value.(*cube.MessageReportOutput), nil
}
func messageCubeRuntime(t *testing.T, db *sql.DB, subject string, internal, provided bool) *druntime.Runtime {
	resources := resource.New()
	must(t, resources.Register(cube.ReaderDatlyResourceNamespace, cube.ReaderDatlyResources))
	base := payloadArtifact(t, resources, reflect.TypeFor[cube.ReaderComponent](), reflect.TypeFor[cube.MessageReportInput](), reflect.TypeFor[cube.MessageReportOutput]())
	compiled, err := report.NewProjectCompiler(report.ProjectConfig{}).CompileArtifacts([]bootstrap.ArtifactInput{{Component: base.Component, InputType: reflect.TypeFor[cube.MessageReportInput](), OutputType: reflect.TypeFor[cube.MessageReportOutput](), Resources: resources}})
	must(t, err)
	providers := []locator.Provider{}
	if provided {
		providers = append(providers, ordinaryAccess("messageaccess", func(context.Context, reflect.Type, string) (any, bool, error) { return internal, true, nil }), provider.Named("visibility", func(context.Context, reflect.Type, string) (any, bool, error) { return &subject, true, nil }))
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
