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

	sessionread "github.com/viant/agently-core/internal/datly/session/read"
	sessiondelete "github.com/viant/agently-core/internal/datly/session/write"
	"github.com/viant/bindly/locator"
	requestprovider "github.com/viant/bindly/provider/request"
	"github.com/viant/bindly/resource"
	"github.com/viant/datly/bootstrap"
	dexec "github.com/viant/datly/exec"
	druntime "github.com/viant/datly/runtime"
	writer "github.com/viant/datly/runtime/handler/writer"
	"github.com/viant/datly/runtime/registry"
	"github.com/viant/datly/spec"
	dsql "github.com/viant/datly/sql"
	"github.com/viant/datly/sql/dml"
	viewprovider "github.com/viant/datly/sql/reader/provider"
)

// The generated component opts into delete_not_found(ignore), while the framework
// default remains strict. Stored state and acknowledgments are compared with legacy.
func TestSessionDeletionLegacyV1Parity(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	legacy := legacyProbeBinary(t, project)
	type expect struct {
		legacyIDs, newIDs []string
		newFails          bool
		legacyFails       bool
		reject            bool
	}
	type useCase struct {
		desc   string
		input  []string
		expect expect
	}
	for _, tc := range []useCase{
		{"empty request is a no-op", []string{}, expect{legacyIDs: []string{"s1", "s2"}, newIDs: []string{"s1", "s2"}}},
		{"repeated ID remains idempotent", []string{"s1", "s1"}, expect{legacyIDs: []string{"s2"}, newIDs: []string{"s2"}}},
		{"existing ID matches", []string{"s1"}, expect{legacyIDs: []string{"s2"}, newIDs: []string{"s2"}}},
		{"unknown ID is a legacy no-op", []string{"absent"}, expect{legacyIDs: []string{"s1", "s2"}, newIDs: []string{"s1", "s2"}, newFails: false}},
		{"existing and unknown IDs delete matching rows atomically", []string{"s1", "absent"}, expect{legacyIDs: []string{"s2"}, newIDs: []string{"s2"}, newFails: false}},
		{"late delete failure rolls back known and missing batch", []string{"s1", "absent", "s2"}, expect{legacyIDs: []string{"s1", "s2"}, newIDs: []string{"s1", "s2"}, newFails: true, legacyFails: true, reject: true}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			oldDB, oldPath := sessionParityFixture(t, project)
			db, _ := sessionParityFixture(t, project)
			if tc.expect.reject {
				for _, fixture := range []*sql.DB{oldDB, db} {
					_, err := fixture.Exec("CREATE TRIGGER reject_session_delete BEFORE DELETE ON session WHEN OLD.id='s2' BEGIN SELECT RAISE(ABORT,'fixture delete rejection'); END;")
					must(t, err)
				}
			}
			request := struct {
				Component, DBPath string
				Filters           map[string]any
			}{"sessionDelete", oldPath, map[string]any{"ids": tc.input}}
			encoded, err := json.Marshal(request)
			must(t, err)
			process := exec.Command(legacy)
			process.Stdin = bytes.NewReader(encoded)
			var stderr bytes.Buffer
			process.Stderr = &stderr
			output, err := process.Output()
			if err != nil {
				t.Fatalf("legacy execution: %v\n%s", err, stderr.String())
			}
			var old probeResult
			must(t, json.Unmarshal(output, &old))
			if old.Failed != tc.expect.legacyFails {
				t.Fatalf("legacy deletion failed: %s", old.Error)
			}
			var acknowledged []string
			must(t, json.Unmarshal(old.Output, &acknowledged))
			if !tc.expect.legacyFails && len(tc.input) > 0 && !reflect.DeepEqual(acknowledged, tc.input) {
				t.Fatalf("legacy acknowledgment=%v expected=%v", acknowledged, tc.input)
			}
			resources := resource.New()
			must(t, resources.Register(sessionread.ReaderDatlyResourceNamespace, sessionread.ReaderDatlyResources))
			must(t, resources.Register(sessiondelete.WriterDatlyResourceNamespace, sessiondelete.WriterDatlyResources))
			ra := payloadArtifact(t, resources, reflect.TypeFor[sessionread.ReaderComponent](), reflect.TypeFor[sessionread.SessionInput](), reflect.TypeFor[sessionread.SessionOutput]())
			da := payloadArtifact(t, resources, reflect.TypeFor[sessiondelete.WriterComponent](), reflect.TypeFor[sessiondelete.Input](), reflect.TypeFor[sessiondelete.Output]())
			reader, err := ra.ReaderCompilation().NewExecution(bootstrap.ReaderRuntimeConfig{SQL: &dsql.SQLComponent{DB: db}})
			must(t, err)
			views, err := viewprovider.New(viewprovider.Config{Dependencies: da.ViewDependencies, Input: da.Input, SQL: &dsql.SQLComponent{DB: db}})
			must(t, err)
			handler, err := writer.New(da.Component, reflect.TypeFor[sessiondelete.Input](), reflect.TypeFor[sessiondelete.Output](), "patch")
			must(t, err)
			rt, err := druntime.NewRuntime([]*registry.RegisteredComponent{
				{Component: ra.Component, Input: ra.Input, Output: ra.Output, OutputType: reflect.TypeFor[sessionread.SessionOutput](), Reader: reader},
				{Component: da.Component, Input: da.Input, Output: da.Output, OutputType: reflect.TypeFor[sessiondelete.Output](), Handler: handler, Providers: []locator.Provider{views}, DataSource: dml.Source{DB: db}},
			}, druntime.WithResources(resources))
			must(t, err)
			rows := []map[string]any{}
			for _, id := range tc.input {
				rows = append(rows, map[string]any{"id": id, "shouldDelete": true})
			}
			body, err := json.Marshal(map[string]any{"data": rows})
			must(t, err)
			req := httptest.NewRequest("PATCH", "/v1/api/agently/user/session", strings.NewReader(string(body)))
			req.Header.Set("Content-Type", "application/json")
			scope, err := requestprovider.New(req)
			must(t, err)
			defer scope.Close()
			_, err = rt.ExecuteRoute(context.Background(), "PATCH", "/v1/api/agently/user/session", scope)
			if (err != nil) != tc.expect.newFails {
				t.Fatalf("native deletion error=%v expected failure=%v", err, tc.expect.newFails)
			}
			if tc.expect.newFails && !tc.expect.reject && !strings.Contains(err.Error(), "matched complete identity") {
				t.Fatalf("unexpected divergence: %v", err)
			}
			result, err := rt.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: ra.Component.Key, Route: spec.RouteRef{Method: "GET", Path: "/v1/api/agently/user/session"}}, Input: &sessionread.SessionInput{}})
			must(t, err)
			encoded, err = json.Marshal(result.(*sessionread.SessionOutput).Data)
			must(t, err)
			var current []json.RawMessage
			must(t, json.Unmarshal(encoded, &current))
			ids := func(rows []json.RawMessage) []string {
				values := []string{}
				for _, row := range normalizeRows(t, rows) {
					values = append(values, row["id"].(string))
				}
				return values
			}
			if !reflect.DeepEqual(ids(old.Rows), tc.expect.legacyIDs) || !reflect.DeepEqual(ids(current), tc.expect.newIDs) {
				t.Fatalf("legacy=%v native=%v", ids(old.Rows), ids(current))
			}
		})
	}
}
