package tests

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"github.com/viant/agently-core/internal/datly/queryselectors"
	read "github.com/viant/agently-core/internal/datly/reporting/sharedartifact/read"
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
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestForgeReaderLegacyParity(t *testing.T) {
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
		{"list order by activity", input{"rows", nil}, []string{"other", "second", "existing"}},
		{"owner list", input{"rows", map[string]any{"ownerId": "u1"}}, []string{"second", "existing"}},
		{"artifact reference filter", input{"rows", map[string]any{"artifactRef": "report://existing"}}, []string{"existing"}},
		{"report ID filter", input{"rows", map[string]any{"reportId": "report-two"}}, []string{"second"}},
		{"kind filter", input{"rows", map[string]any{"kind": "report"}}, []string{"other", "second", "existing"}},
		{"lifecycle filter", input{"rows", map[string]any{"lifecycle": "saved"}}, []string{"second", "existing"}},
		{"by ID", input{"byId", map[string]any{"artifactId": "existing"}}, []string{"existing"}},
		{"missing ID", input{"byId", map[string]any{"artifactId": "missing"}}, []string{}},
		{"by ID owner filter", input{"byId", map[string]any{"artifactId": "existing", "ownerId": "u2"}}, []string{}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			_, path := forgeReaderFixture(t, project)
			db, _ := forgeReaderFixture(t, project)
			filters := map[string]any{"mode": tc.input.mode}
			for k, v := range tc.input.filters {
				filters[k] = v
			}
			payload, err := json.Marshal(map[string]any{"Component": "forgeReader", "DBPath": path, "Filters": filters})
			must(t, err)
			cmd := exec.Command(legacy)
			cmd.Stdin = bytes.NewReader(payload)
			raw, err := cmd.Output()
			must(t, err)
			var before probeResult
			must(t, json.Unmarshal(raw, &before))
			if before.Failed {
				t.Fatalf("legacy: %s", before.Error)
			}
			rt, key := forgeReaderRuntime(t, db, "", tc.input.mode, true, true)
			out, err := rt.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "GET", Path: "/v1/internal/forge/reporting/shared-artifact"}}, Input: forgeReadInput(t, tc.input.filters)})
			must(t, err)
			raw, err = json.Marshal(out.(*read.Output).Data)
			must(t, err)
			var rows []json.RawMessage
			must(t, json.Unmarshal(raw, &rows))
			oldRows, newRows := normalizeRowsInOrder(t, before.Rows), normalizeRowsInOrder(t, rows)
			if tc.input.mode == "rows" {
				oldRows = normalizeForgeRowsForComparison(oldRows)
				newRows = normalizeForgeRowsForComparison(newRows)
			}
			if !reflect.DeepEqual(oldRows, newRows) {
				t.Fatalf("legacy=%s native=%s", pretty(oldRows), pretty(newRows))
			}
			ids := []string{}
			for _, row := range newRows {
				ids = append(ids, row["artifactid"].(string))
			}
			if !reflect.DeepEqual(ids, tc.expect) {
				t.Fatalf("ids=%v expected=%v", ids, tc.expect)
			}
		})
	}
}
func forgeReaderFixture(t *testing.T, project string) (*sql.DB, string) {
	db, path := forgeFixture(t, project)
	_, err := db.Exec(`UPDATE report_shared_artifact SET owner_ref='',report_id='',source_artifact_id='',base_artifact_ref='',policy_ref='' WHERE artifact_id='existing';
 INSERT INTO report_shared_artifact(artifact_id,artifact_ref,owner_id,owner_ref,kind,lifecycle,version,report_id,title,source_artifact_id,base_artifact_ref,policy_ref,report_document_json,created_at,updated_at) VALUES
 ('second','report://second','u1','','report','saved',1,'report-two','second','','','',X'7B7D','2026-01-02 00:00:00','2026-01-03 00:00:00'),
 ('other','report://other','u2','','report','draft',1,'','other','','','',X'7B7D','2026-01-03 00:00:00','2026-01-04 00:00:00');`)
	must(t, err)
	return db, path
}
func normalizeForgeRowsForComparison(rows []map[string]any) []map[string]any { return rows }
func forgeReadInput(t *testing.T, filters map[string]any) *read.Input {
	input := &read.Input{Has: &read.InputHas{}}
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
func forgeReaderRuntime(t *testing.T, db *sql.DB, subject, mode string, internal, provided bool) (*druntime.Runtime, spec.Key) {
	resources := resource.New()
	must(t, resources.Register(read.ReaderDatlyResourceNamespace, read.ReaderDatlyResources))
	artifact := payloadArtifact(t, resources, reflect.TypeFor[read.ReaderComponent](), reflect.TypeFor[read.Input](), reflect.TypeFor[read.Output]())
	execution, err := artifact.ReaderCompilation().NewExecution(bootstrap.ReaderRuntimeConfig{SQL: &dsql.SQLComponent{DB: db}})
	must(t, err)
	providers := []locator.Provider{}
	if provided {
		providers = append(providers, ordinaryAccess("artifactaccess", func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
			if name == "mode" {
				return mode, true, nil
			}
			return internal, true, nil
		}), provider.Named("visibility", func(context.Context, reflect.Type, string) (any, bool, error) { return &subject, true, nil }))
	}
	rt, err := druntime.NewRuntime([]*registry.RegisteredComponent{{Component: artifact.Component, Input: artifact.Input, Output: artifact.Output, OutputType: reflect.TypeFor[read.Output](), Reader: execution, Providers: providers}}, druntime.WithResources(resources))
	must(t, err)
	return rt, artifact.Component.Key
}
func TestForgeReaderScopeAndSelectors(t *testing.T) {
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
		{"anonymous sees no private artifact", input{"", "rows", false, true, nil}, expect{ids: []string{}}},
		{"owner sees own artifacts", input{"u1", "rows", false, true, nil}, expect{ids: []string{"second", "existing"}}},
		{"other owner is isolated", input{"u2", "rows", false, true, nil}, expect{ids: []string{"other"}}},
		{"owner query cannot bypass subject", input{"u2", "rows", false, true, map[string]any{"ownerId": "u1"}}, expect{ids: []string{}}},
		{"by-ID private scope", input{"u2", "byId", false, true, map[string]any{"artifactId": "existing"}}, expect{ids: []string{}}},
		{"missing host fails", input{"", "rows", false, false, nil}, expect{failed: true}},
		{"unsupported host mode fails", input{"", "bad", true, true, nil}, expect{failed: true}},
		{"pagination and projection", input{"u1", "rows", false, true, map[string]any{"orderBy": "artifact_id ASC", "limit": 1, "offset": 1, "fields": []string{"artifact_id"}}}, expect{ids: []string{"second"}}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			db, _ := forgeReaderFixture(t, project)
			rt, key := forgeReaderRuntime(t, db, tc.input.subject, tc.input.mode, tc.input.internal, tc.input.provided)
			request := forgeReadInput(t, tc.input.filters)
			out, err := rt.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "GET", Path: "/v1/internal/forge/reporting/shared-artifact"}}, Input: request})
			if (err != nil) != tc.expect.failed {
				t.Fatalf("failure=%v expected=%v", err, tc.expect.failed)
			}
			if err != nil {
				return
			}
			ids := []string{}
			for _, row := range out.(*read.Output).Data {
				ids = append(ids, row.ArtifactId)
				if request.Has.Fields && (row.OwnerId != "" || row.Title != "") {
					t.Fatal("projection overfetched")
				}
			}
			if !reflect.DeepEqual(ids, tc.expect.ids) {
				t.Fatalf("ids=%v expected=%v", ids, tc.expect.ids)
			}
		})
	}
	t.Run("HTTP cannot override owner", func(t *testing.T) {
		db, _ := forgeReaderFixture(t, project)
		rt, _ := forgeReaderRuntime(t, db, "u2", "rows", false, true)
		req := httptest.NewRequest("GET", "/v1/internal/forge/reporting/shared-artifact?internal=true&ownerSubject=u1&ownerId=u1&readMode=byId", nil)
		scope, err := requestprovider.New(req)
		must(t, err)
		defer scope.Close()
		out, err := rt.ExecuteRoute(context.Background(), "GET", "/v1/internal/forge/reporting/shared-artifact", scope)
		must(t, err)
		if len(out.(*read.Output).Data) != 0 {
			t.Fatal("HTTP widened owner scope")
		}
	})
	t.Run("legacy selector proxy", func(t *testing.T) {
		db, _ := forgeReaderFixture(t, project)
		rt, key := forgeReaderRuntime(t, db, "u1", "rows", false, true)
		proxy := queryselectors.ProviderMapped(state.Selectors{&state.NamedSelector{Name: "forge_report_shared_artifact_list", Selector: state.Selector{Fields: []string{"artifact_id"}, OrderBy: "artifact_id ASC", Limit: 1}}}, map[string]string{"forge_report_shared_artifact_list": "reader"})
		out, err := rt.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "GET", Path: "/v1/internal/forge/reporting/shared-artifact"}}, Input: forgeReadInput(t, nil), Providers: []locator.Provider{proxy}})
		must(t, err)
		rows := out.(*read.Output).Data
		if len(rows) != 1 || rows[0].ArtifactId != "existing" || rows[0].Title != "" {
			t.Fatalf("proxy scope/projection=%s", pretty(rows))
		}
	})
	t.Run("nullable text remains business empty string", func(t *testing.T) {
		db, _ := forgeReaderFixture(t, project)
		_, err := db.Exec("UPDATE report_shared_artifact SET owner_ref=NULL WHERE artifact_id='existing'")
		must(t, err)
		rt, key := forgeReaderRuntime(t, db, "u1", "byId", false, true)
		out, err := rt.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "GET", Path: "/v1/internal/forge/reporting/shared-artifact"}}, Input: forgeReadInput(t, map[string]any{"artifactId": "existing"})})
		must(t, err)
		rows := out.(*read.Output).Data
		if len(rows) != 1 || rows[0].OwnerRef != "" {
			t.Fatal("nullable owner reference was not normalized")
		}
	})
}
