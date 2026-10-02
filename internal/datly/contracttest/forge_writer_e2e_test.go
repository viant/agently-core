package tests

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/viant/datly/runtime/handler/provider"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	write "github.com/viant/agently-core/internal/datly/reporting/sharedartifact/write"
	"github.com/viant/bindly/locator"
	requestprovider "github.com/viant/bindly/provider/request"
	"github.com/viant/bindly/resource"
	dexec "github.com/viant/datly/exec"
	druntime "github.com/viant/datly/runtime"
	writer "github.com/viant/datly/runtime/handler/writer"
	"github.com/viant/datly/runtime/registry"
	"github.com/viant/datly/spec"
	dsql "github.com/viant/datly/sql"
	"github.com/viant/datly/sql/dml"
	viewprovider "github.com/viant/datly/sql/reader/provider"
)

func TestForgeWriterIntentLegacyStoreContract(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type input struct {
		intent, id, owner string
		delete            bool
	}
	type expect struct {
		failure error
		count   int
		title   string
	}
	type useCase struct {
		desc   string
		input  input
		expect expect
	}
	for _, tc := range []useCase{
		{"create missing artifact", input{intent: "create", id: "new", owner: "u1"}, expect{count: 1, title: "changed"}},
		{"create existing artifact fails", input{intent: "create", id: "existing", owner: "u1"}, expect{failure: write.ErrAlreadyExists, count: 1, title: "old"}},
		{"update existing artifact", input{intent: "update", id: "existing", owner: "u1"}, expect{count: 1, title: "changed"}},
		{"update missing artifact fails", input{intent: "update", id: "new", owner: "u1"}, expect{failure: write.ErrNotFound}},
		{"default upsert retains existing behavior", input{id: "new", owner: "u1"}, expect{count: 1, title: "changed"}},
		{"foreign existing artifact does not leak create conflict", input{intent: "create", id: "existing", owner: "u2"}, expect{failure: errors.New("owner scope"), count: 1, title: "old"}},
		{"create intent cannot delete", input{intent: "create", id: "existing", owner: "u1", delete: true}, expect{failure: errors.New("create intent"), count: 1, title: "old"}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			db, _ := forgeFixture(t, project)
			rt := forgeWriterRuntime(t, db, "u1", false)
			artifact := &write.SharedArtifact{}
			artifact.SetArtifactId(tc.input.id)
			artifact.SetOwnerId(tc.input.owner)
			artifact.SetTitle("changed")
			if tc.input.id == "new" {
				artifact.SetArtifactRef("report://new")
				artifact.SetKind("report")
				artifact.SetLifecycle("saved")
			}
			if tc.input.delete {
				artifact.SetShouldDelete(true)
			}
			in := &write.Input{}
			in.SetArtifact(artifact)
			if tc.input.intent != "" {
				in.SetWriteIntent(tc.input.intent)
			}
			key := spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[write.WriterComponent]().PkgPath(), Name: "writer"}
			_, err := rt.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "PATCH", Path: "/v1/api/forge/reporting/shared-artifact"}}, Input: in})
			if tc.expect.failure == nil && err != nil {
				t.Fatalf("unexpected mutation failure: %v", err)
			}
			if tc.expect.failure != nil && err == nil {
				t.Fatalf("expected %v", tc.expect.failure)
			}
			if tc.expect.failure == write.ErrAlreadyExists || tc.expect.failure == write.ErrNotFound {
				if !errors.Is(err, tc.expect.failure) {
					t.Fatalf("failure %v does not preserve %v", err, tc.expect.failure)
				}
			} else if tc.expect.failure != nil && errors.Is(err, write.ErrAlreadyExists) {
				t.Fatalf("foreign or invalid request leaked duplicate classification: %v", err)
			}
			var count int
			var title string
			must(t, db.QueryRow("SELECT COUNT(*), COALESCE(MAX(title), '') FROM report_shared_artifact WHERE artifact_id=?", tc.input.id).Scan(&count, &title))
			if count != tc.expect.count || title != tc.expect.title {
				t.Fatalf("persisted count/title=(%d,%q), want (%d,%q)", count, title, tc.expect.count, tc.expect.title)
			}
		})
	}
}

func TestForgeWriterLegacyParity(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	legacy := legacyProbeBinary(t, project)
	type expect struct{ failed, corrected bool }
	type useCase struct {
		desc, input string
		expect      expect
	}
	for _, tc := range []useCase{
		{"insert required report shell", `{"data":{"artifactId":"new","artifactRef":"report://new","ownerId":"u1","kind":"report","lifecycle":"draft"}}`, expect{}},
		{"insert JSON blobs", `{"data":{"artifactId":"new","artifactRef":"report://new","ownerId":"u1","kind":"report","lifecycle":"draft","document":[123,125],"reportSpec":[123,34,120,34,58,49,125],"metadata":[123,125]}}`, expect{}},
		{"full update changes report and version", `{"data":{"artifactId":"existing","artifactRef":"report://updated","ownerId":"u1","ownerRef":"","reportId":"","sourceArtifactId":"","baseArtifactRef":"","policyRef":"","kind":"report","lifecycle":"saved","version":3,"title":"updated","document":[123,125],"reportSpec":[123,125],"metadata":[123,125]}}`, expect{}},
		{"sparse update preserves omitted report fields", `{"data":{"artifactId":"existing","ownerId":"u1","title":"updated"}}`, expect{corrected: true}},
		{"explicit null document clears", `{"data":{"artifactId":"existing","artifactRef":"report://existing","ownerId":"u1","ownerRef":"","reportId":"","sourceArtifactId":"","baseArtifactRef":"","policyRef":"","kind":"report","lifecycle":"saved","version":2,"title":"old","document":null,"reportSpec":[123,125],"metadata":[123,125]}}`, expect{}},
		{"supplied timestamps retained", `{"data":{"artifactId":"existing","artifactRef":"report://existing","ownerId":"u1","ownerRef":"","reportId":"","sourceArtifactId":"","baseArtifactRef":"","policyRef":"","kind":"report","lifecycle":"saved","version":2,"title":"old","document":[123,125],"reportSpec":[123,125],"metadata":[123,125],"createdAt":"2026-01-01T00:00:00Z","updatedAt":"2026-01-01T00:00:00Z"}}`, expect{}},
		{"missing body rejected", `{"data":null}`, expect{failed: true}},
		{"blank artifact ID rejected", `{"data":{"artifactId":" ","ownerId":"u1"}}`, expect{failed: true}},
		{"blank owner rejected", `{"data":{"artifactId":"new","ownerId":" "}}`, expect{failed: true}},
		{"missing artifact reference stores legacy empty value", `{"data":{"artifactId":"new","ownerId":"u1","kind":"report","lifecycle":"draft"}}`, expect{}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			oldDB, path := forgeFixture(t, project)
			db, _ := forgeFixture(t, project)
			payload, err := json.Marshal(map[string]any{"Component": "forgeWriter", "DBPath": path, "Body": tc.input})
			must(t, err)
			process := exec.Command(legacy)
			process.Stdin = bytes.NewReader(payload)
			raw, err := process.Output()
			must(t, err)
			var before probeResult
			must(t, json.Unmarshal(raw, &before))
			request := httptest.NewRequest("PATCH", "/v1/api/forge/reporting/shared-artifact", strings.NewReader(tc.input))
			request.Header.Set("Content-Type", "application/json")
			scope, err := requestprovider.New(request)
			must(t, err)
			defer scope.Close()
			out, err := forgeWriterRuntime(t, db, "u1", false).ExecuteRoute(context.Background(), "PATCH", "/v1/api/forge/reporting/shared-artifact", scope)
			if before.Failed != tc.expect.failed || (err != nil) != tc.expect.failed {
				t.Fatalf("legacy failure=%v (%s) native=%v expected=%v", before.Failed, before.Error, err, tc.expect.failed)
			}
			oldRows, newRows := forgeStoredRows(t, oldDB), forgeStoredRows(t, db)
			if tc.expect.corrected {
				if reflect.DeepEqual(oldRows, newRows) {
					t.Fatal("legacy sparse overwrite was not demonstrated")
				}
				if newRows[0]["artifactref"] != "report://existing" || newRows[0]["kind"] != "report" || newRows[0]["reportdocumentjson"] != "{}" {
					t.Fatalf("native lost omitted report fields: %s", pretty(newRows))
				}
			} else if !reflect.DeepEqual(oldRows, newRows) {
				t.Fatalf("stored legacy=%s native=%s", pretty(oldRows), pretty(newRows))
			}
			if !tc.expect.failed && !tc.expect.corrected {
				var oldOutput map[string]any
				must(t, json.Unmarshal(before.Output, &oldOutput))
				raw, err = json.Marshal(out.(*write.Output).Data)
				must(t, err)
				var newOutput map[string]any
				must(t, json.Unmarshal(raw, &newOutput))
				if !reflect.DeepEqual(normalizeForgeResponse(oldOutput), normalizeForgeResponse(newOutput)) {
					t.Fatalf("response legacy=%s native=%s", before.Output, raw)
				}
			}
		})
	}
}
func forgeFixture(t *testing.T, project string) (*sql.DB, string) {
	db, path := goalFixture(t, project)
	_, err := db.Exec(`INSERT INTO report_shared_artifact(artifact_id,artifact_ref,owner_id,kind,lifecycle,version,title,report_document_json,report_spec_json,metadata_json,created_at,updated_at) VALUES('existing','report://existing','u1','report','saved',2,'old',X'7B7D',X'7B7D',X'7B7D','2026-01-01 00:00:00','2026-01-02 00:00:00')`)
	must(t, err)
	return db, path
}
func forgeStoredRows(t *testing.T, db *sql.DB) []map[string]any {
	rows, err := db.Query("SELECT * FROM report_shared_artifact ORDER BY artifact_id")
	must(t, err)
	defer rows.Close()
	columns, err := rows.Columns()
	must(t, err)
	result := []map[string]any{}
	for rows.Next() {
		values := make([]any, len(columns))
		targets := make([]any, len(columns))
		for i := range values {
			targets[i] = &values[i]
		}
		must(t, rows.Scan(targets...))
		row := map[string]any{}
		for i, name := range columns {
			value := values[i]
			if b, ok := value.([]byte); ok {
				value = string(b)
			}
			row[strings.ReplaceAll(name, "_", "")] = value
		}
		for _, key := range []string{"createdat", "updatedat"} {
			if row[key] != nil {
				if row[key] == "2026-01-01 00:00:00" || row[key] == "2026-01-01T00:00:00Z" {
					row[key] = "<baseline timestamp>"
				} else {
					row[key] = "<generated timestamp>"
				}
			}
		}
		result = append(result, row)
	}
	must(t, rows.Err())
	return result
}
func normalizeForgeResponse(row map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range row {
		key := strings.ToLower(k)
		if key == "createdat" || key == "updatedat" {
			if v != nil {
				if v == "2026-01-01T00:00:00Z" {
					v = "<baseline timestamp>"
				} else {
					v = "<generated timestamp>"
				}
			}
		}
		out[key] = v
	}
	return out
}
func forgeWriterRuntime(t *testing.T, db *sql.DB, subject string, internal bool, supplied ...*sql.Tx) *druntime.Runtime {
	resources := resource.New()
	must(t, resources.Register(write.WriterDatlyResourceNamespace, write.WriterDatlyResources))
	artifact := payloadArtifact(t, resources, reflect.TypeFor[write.WriterComponent](), reflect.TypeFor[write.Input](), reflect.TypeFor[write.Output]())
	views, err := viewprovider.New(viewprovider.Config{Dependencies: artifact.ViewDependencies, Input: artifact.Input, SQL: &dsql.SQLComponent{DB: db}})
	must(t, err)
	handler, err := writer.New(artifact.Component, reflect.TypeFor[write.Input](), reflect.TypeFor[write.Output](), "patch")
	must(t, err)
	var tx *sql.Tx
	if len(supplied) > 0 {
		tx = supplied[0]
	}
	rt, err := druntime.NewRuntime([]*registry.RegisteredComponent{{Component: artifact.Component, Input: artifact.Input, Output: artifact.Output, OutputType: reflect.TypeFor[write.Output](), Handler: handler, Providers: []locator.Provider{views, ordinaryAccess("artifactaccess", func(context.Context, reflect.Type, string) (any, bool, error) { return internal, true, nil }), provider.Named("visibility", func(context.Context, reflect.Type, string) (any, bool, error) {
		if internal {
			return nil, false, nil
		}
		return &subject, true, nil
	})}, DataSource: dml.Source{DB: db, Tx: tx}}}, druntime.WithResources(resources))
	must(t, err)
	return rt
}

func TestForgeWriterOwnerAndDelete(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type input struct {
		subject  string
		internal bool
		body     string
	}
	type expect struct {
		failed bool
		exists int
	}
	type useCase struct {
		desc   string
		input  input
		expect expect
	}
	for _, tc := range []useCase{
		{"owner creates own artifact", input{"u1", false, `{"data":{"artifactId":"new","artifactRef":"report://new","ownerId":"u1","kind":"report","lifecycle":"draft"}}`}, expect{exists: 1}},
		{"anonymous creation rejected", input{"", false, `{"data":{"artifactId":"new","artifactRef":"report://new","ownerId":"u1","kind":"report","lifecycle":"draft"}}`}, expect{failed: true}},
		{"caller cannot create for another owner", input{"u2", false, `{"data":{"artifactId":"new","artifactRef":"report://new","ownerId":"u1","kind":"report","lifecycle":"draft"}}`}, expect{failed: true}},
		{"owner updates own artifact", input{"u1", false, `{"data":{"artifactId":"existing","ownerId":"u1","title":"updated"}}`}, expect{exists: 1}},
		{"caller cannot seize another artifact", input{"u2", false, `{"data":{"artifactId":"existing","ownerId":"u2","title":"seized"}}`}, expect{failed: true, exists: 1}},
		{"owner deletes artifact", input{"u1", false, `{"data":{"artifactId":"existing","ownerId":"u1","shouldDelete":true}}`}, expect{}},
		{"foreign delete rejected", input{"u2", false, `{"data":{"artifactId":"existing","ownerId":"u2","shouldDelete":true}}`}, expect{failed: true, exists: 1}},
		{"unknown delete remains strict", input{"u1", false, `{"data":{"artifactId":"absent","ownerId":"u1","shouldDelete":true}}`}, expect{failed: true, exists: 1}},
		{"trusted internal delete", input{"", true, `{"data":{"artifactId":"existing","ownerId":"u1","shouldDelete":true}}`}, expect{}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			db, _ := forgeFixture(t, project)
			req := httptest.NewRequest("PATCH", "/v1/api/forge/reporting/shared-artifact", strings.NewReader(tc.input.body))
			req.Header.Set("Content-Type", "application/json")
			scope, err := requestprovider.New(req)
			must(t, err)
			defer scope.Close()
			_, err = forgeWriterRuntime(t, db, tc.input.subject, tc.input.internal).ExecuteRoute(context.Background(), "PATCH", "/v1/api/forge/reporting/shared-artifact", scope)
			if (err != nil) != tc.expect.failed {
				t.Fatalf("failure=%v expected=%v", err, tc.expect.failed)
			}
			id := "existing"
			if strings.Contains(tc.input.body, `"artifactId":"new"`) {
				id = "new"
			}
			var count int
			must(t, db.QueryRow("SELECT COUNT(*) FROM report_shared_artifact WHERE artifact_id=?", id).Scan(&count))
			if count != tc.expect.exists {
				t.Fatalf("count=%d expected=%d", count, tc.expect.exists)
			}
		})
	}
}
func TestForgeWriterCallerTransaction(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type useCase struct {
		desc   string
		commit bool
		expect string
	}
	for _, tc := range []useCase{{"caller rollback", false, "old"}, {"caller commit", true, "updated"}} {
		t.Run(tc.desc, func(t *testing.T) {
			db, _ := forgeFixture(t, project)
			tx, err := db.BeginTx(context.Background(), nil)
			must(t, err)
			defer tx.Rollback()
			req := httptest.NewRequest("PATCH", "/v1/api/forge/reporting/shared-artifact", strings.NewReader(`{"data":{"artifactId":"existing","ownerId":"u1","title":"updated"}}`))
			req.Header.Set("Content-Type", "application/json")
			scope, err := requestprovider.New(req)
			must(t, err)
			defer scope.Close()
			_, err = forgeWriterRuntime(t, db, "u1", false, tx).ExecuteRoute(context.Background(), "PATCH", "/v1/api/forge/reporting/shared-artifact", scope)
			must(t, err)
			var pending string
			must(t, tx.QueryRow("SELECT title FROM report_shared_artifact WHERE artifact_id='existing'").Scan(&pending))
			if pending != "updated" {
				t.Fatal("caller update not pending")
			}
			if tc.commit {
				must(t, tx.Commit())
			} else {
				must(t, tx.Rollback())
			}
			var title string
			must(t, db.QueryRow("SELECT title FROM report_shared_artifact WHERE artifact_id='existing'").Scan(&title))
			if title != tc.expect {
				t.Fatalf("title=%s expected=%s", title, tc.expect)
			}
		})
	}
}

func TestForgeWriterOutputOwnsBinaryFields(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	db, _ := forgeFixture(t, project)
	rt := forgeWriterRuntime(t, db, "u1", false)
	artifact := &write.SharedArtifact{}
	artifact.SetArtifactId("new")
	artifact.SetArtifactRef("report://new")
	artifact.SetOwnerId("u1")
	artifact.SetKind("report")
	artifact.SetLifecycle("draft")
	artifact.SetReportDocumentJson([]byte("{}"))
	key := spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[write.WriterComponent]().PkgPath(), Name: "writer"}
	out, err := rt.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "PATCH", Path: "/v1/api/forge/reporting/shared-artifact"}}, Input: &write.Input{Artifact: artifact}})
	must(t, err)
	payload := out.(*write.Output).Data
	if payload == nil || string(payload.ReportDocumentJson) != "{}" {
		t.Fatal("binary output missing")
	}
	payload.ReportDocumentJson[0] = 'X'
	if string(artifact.ReportDocumentJson) != "{}" {
		t.Fatal("output aliases mutable input bytes")
	}
	var stored string
	must(t, db.QueryRow("SELECT report_document_json FROM report_shared_artifact WHERE artifact_id='new'").Scan(&stored))
	if stored != "{}" {
		t.Fatalf("stored payload=%s", stored)
	}
}
