package tests

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"github.com/viant/datly/runtime/handler/provider"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	read "github.com/viant/agently-core/internal/datly/reporting/artifact/read"
	write "github.com/viant/agently-core/internal/datly/reporting/artifact/write"
	jobread "github.com/viant/agently-core/internal/datly/reporting/job/read"
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

func reportArtifactFixture(t *testing.T, project string) (*sql.DB, string) {
	t.Helper()
	db, path := reportJobFixture(t, project)
	_, err := db.Exec(`INSERT INTO report_export_artifact(artifact_id,job_id,artifact_ref,owner_id,format,content_type,inline_data,created_at,retention_ttl_sec) VALUES
 ('artifact-running','running','report://running','u1','pdf','application/pdf',X'25504446','2026-01-02 00:00:00',120),
 ('artifact-foreign','foreign','report://foreign','u2','pdf','application/pdf',X'25504446','2026-01-03 00:00:00',60)`)
	must(t, err)
	return db, path
}

func reportArtifactRuntime(t *testing.T, db *sql.DB, subject string, internal bool, supplied *sql.Tx) (*druntime.Runtime, spec.Key, spec.Key) {
	t.Helper()
	resources := resource.New()
	must(t, resources.Register(read.ReaderDatlyResourceNamespace, read.ReaderDatlyResources))
	must(t, resources.Register(write.WriterDatlyResourceNamespace, write.WriterDatlyResources))
	must(t, resources.Register(jobread.ReaderDatlyResourceNamespace, jobread.ReaderDatlyResources))
	ar := payloadArtifact(t, resources, reflect.TypeFor[read.ReaderComponent](), reflect.TypeFor[read.Input](), reflect.TypeFor[read.Output]())
	aw := payloadArtifact(t, resources, reflect.TypeFor[write.WriterComponent](), reflect.TypeFor[write.Input](), reflect.TypeFor[write.Output]())
	jr := payloadArtifact(t, resources, reflect.TypeFor[jobread.ReaderComponent](), reflect.TypeFor[jobread.Input](), reflect.TypeFor[jobread.Output]())
	artifactReader, err := ar.ReaderCompilation().NewExecution(bootstrap.ReaderRuntimeConfig{SQL: &dsql.SQLComponent{DB: db}})
	must(t, err)
	jobReader, err := jr.ReaderCompilation().NewExecution(bootstrap.ReaderRuntimeConfig{SQL: &dsql.SQLComponent{DB: db}})
	must(t, err)
	views, err := viewprovider.New(viewprovider.Config{Dependencies: aw.ViewDependencies, Input: aw.Input, SQL: &dsql.SQLComponent{DB: db}})
	must(t, err)
	handler, err := writer.New(aw.Component, reflect.TypeFor[write.Input](), reflect.TypeFor[write.Output](), "patch")
	must(t, err)
	access := ordinaryAccess("reportaccess", func(context.Context, reflect.Type, string) (any, bool, error) { return internal, true, nil })
	visibility := provider.Named("visibility", func(context.Context, reflect.Type, string) (any, bool, error) { return &subject, true, nil })
	rt, err := druntime.NewRuntime([]*registry.RegisteredComponent{
		{Component: ar.Component, Input: ar.Input, Output: ar.Output, OutputType: reflect.TypeFor[read.Output](), Reader: artifactReader, Providers: []locator.Provider{access, visibility}},
		{Component: jr.Component, Input: jr.Input, Output: jr.Output, OutputType: reflect.TypeFor[jobread.Output](), Reader: jobReader, Providers: []locator.Provider{access, visibility}},
		{Component: aw.Component, Input: aw.Input, Output: aw.Output, OutputType: reflect.TypeFor[write.Output](), Handler: handler, Providers: []locator.Provider{access, visibility, views}, DataSource: dml.Source{DB: db, Tx: supplied}},
	}, druntime.WithResources(resources))
	must(t, err)
	return rt, ar.Component.Key, aw.Component.Key
}

func invokeReportArtifactWriter(ctx context.Context, rt *druntime.Runtime, key spec.Key, input *write.Input) (*write.Output, error) {
	value, err := rt.InvokeComponent(ctx, dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "PATCH", Path: "/v1/internal/forge/reporting/artifact"}}, Input: input})
	if err != nil {
		return nil, err
	}
	return value.(*write.Output), nil
}

func reportArtifactNew(id, jobID, artifactRef, format string, ttl int64) *write.Artifact {
	row := &write.Artifact{}
	row.SetArtifactId(id)
	row.SetJobId(jobID)
	row.SetArtifactRef(artifactRef)
	row.SetOwnerId("u1")
	row.SetFormat(format)
	row.SetContentType("application/pdf")
	row.SetInlineData([]byte("%PDF"))
	row.SetCreatedAt(time.Date(2026, 1, 4, 0, 0, 0, 0, time.UTC))
	row.SetRetentionTtlSec(ttl)
	return row
}

func TestReportArtifactWriterParentMatch(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	legacy := legacyProbeBinary(t, project)
	type input struct {
		id, jobID, artifactRef, format, subject string
		ttl                                     int64
	}
	type expect struct {
		failure bool
		present bool
		ttl     int64
	}
	type useCase struct {
		desc   string
		input  input
		expect expect
	}
	for _, tc := range []useCase{
		{"matching queued job creates artifact", input{"new", "queued", "report://queued", "pdf", "u1", 90}, expect{present: true, ttl: 90}},
		{"format match ignores case", input{"new", "queued", "report://queued", "PDF", "u1", 90}, expect{present: true, ttl: 90}},
		{"negative retention clamps to zero", input{"new", "queued", "report://queued", "pdf", "u1", -1}, expect{present: true}},
		{"wrong artifact reference rejected", input{"new", "queued", "wrong", "pdf", "u1", 0}, expect{failure: true}},
		{"wrong format rejected", input{"new", "queued", "report://queued", "csv", "u1", 0}, expect{failure: true}},
		{"missing job rejected", input{"new", "absent", "report://queued", "pdf", "u1", 0}, expect{failure: true}},
		{"foreign job denied", input{"new", "foreign", "report://foreign", "pdf", "u1", 0}, expect{failure: true}},
		{"existing artifact ID rejected", input{"artifact-running", "running", "report://running", "pdf", "u1", 0}, expect{failure: true, present: true, ttl: 120}},
		{"second artifact for same job rejected", input{"new", "running", "report://running", "pdf", "u1", 0}, expect{failure: true}},
		{"foreign caller cannot create", input{"new", "queued", "report://queued", "pdf", "u2", 0}, expect{failure: true}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			oldDB, oldPath := reportArtifactFixture(t, project)
			db, _ := reportArtifactFixture(t, project)
			rt, _, key := reportArtifactRuntime(t, db, tc.input.subject, false, nil)
			row := reportArtifactNew(tc.input.id, tc.input.jobID, tc.input.artifactRef, tc.input.format, tc.input.ttl)
			in := &write.Input{}
			in.SetMode("create")
			in.SetArtifacts([]*write.Artifact{row})
			_, err := invokeReportArtifactWriter(context.Background(), rt, key, in)
			if (err != nil) != tc.expect.failure {
				t.Fatalf("writer error=%v expected failure=%v", err, tc.expect.failure)
			}
			body, e := json.Marshal(map[string]any{
				"artifactId": tc.input.id, "jobId": tc.input.jobID,
				"artifactRef": tc.input.artifactRef, "ownerId": "u1", "format": tc.input.format,
				"contentType": "application/pdf", "data": []byte("%PDF"),
				"createdAt": "2026-01-04T00:00:00Z", "retentionTtl": time.Duration(tc.input.ttl) * time.Second,
			})
			must(t, e)
			payload, e := json.Marshal(map[string]any{
				"Component": "reportArtifact", "DBPath": oldPath, "Principal": tc.input.subject,
				"Method": "put", "Body": string(body),
			})
			must(t, e)
			cmd := exec.Command(legacy)
			cmd.Stdin = bytes.NewReader(payload)
			raw, e := cmd.Output()
			must(t, e)
			var before probeResult
			must(t, json.Unmarshal(raw, &before))
			if before.Failed != tc.expect.failure {
				t.Fatalf("legacy failure=%v (%s), native=%v", before.Failed, before.Error, err)
			}
			var ttl int64
			err = db.QueryRow("SELECT retention_ttl_sec FROM report_export_artifact WHERE artifact_id=?", tc.input.id).Scan(&ttl)
			var oldTTL int64
			oldErr := oldDB.QueryRow("SELECT retention_ttl_sec FROM report_export_artifact WHERE artifact_id=?", tc.input.id).Scan(&oldTTL)
			if (err == nil) != (oldErr == nil) || err == nil && ttl != oldTTL {
				t.Fatalf("legacy ttl=(%d,%v) native=(%d,%v)", oldTTL, oldErr, ttl, err)
			}
			if tc.expect.present {
				must(t, err)
				if ttl != tc.expect.ttl {
					t.Fatalf("ttl=%d expected=%d", ttl, tc.expect.ttl)
				}
			} else if err != sql.ErrNoRows {
				t.Fatalf("unexpected artifact ttl=%d err=%v", ttl, err)
			}
		})
	}
}

type reportArtifactSnapshot struct {
	ArtifactID   string        `json:"artifactId,omitempty"`
	JobID        string        `json:"jobId,omitempty"`
	ArtifactRef  string        `json:"artifactRef,omitempty"`
	OwnerID      string        `json:"ownerId,omitempty"`
	Format       string        `json:"format,omitempty"`
	ContentType  string        `json:"contentType,omitempty"`
	Data         []byte        `json:"data,omitempty"`
	CreatedAt    time.Time     `json:"createdAt,omitempty"`
	RetentionTTL time.Duration `json:"retentionTtl,omitempty"`
}

func reportArtifactSnapshotFromView(row *read.Artifact) reportArtifactSnapshot {
	return reportArtifactSnapshot{
		ArtifactID: row.ArtifactId, JobID: row.JobId, ArtifactRef: row.ArtifactRef,
		OwnerID: row.OwnerId, Format: row.Format, ContentType: row.ContentType,
		Data: row.InlineData, CreatedAt: row.CreatedAt,
		RetentionTTL: time.Duration(row.RetentionTtlSec) * time.Second,
	}
}

func TestReportArtifactReaderLegacyParity(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	legacy := legacyProbeBinary(t, project)
	type input struct {
		subject, id, method string
		internal            bool
	}
	type expect struct {
		id            string
		list          []string
		compareLegacy bool
	}
	type useCase struct {
		desc   string
		input  input
		expect expect
	}
	for _, tc := range []useCase{
		{"exact artifact preserves bytes and retention", input{subject: "u1", id: "artifact-running", method: "get"}, expect{id: "artifact-running", compareLegacy: true}},
		{"owner list returns only own artifact", input{subject: "u1", method: "list"}, expect{list: []string{"artifact-running"}, compareLegacy: true}},
		{"foreign artifact stays hidden", input{subject: "u2", id: "artifact-running", method: "get"}, expect{compareLegacy: true}},
		{"anonymous list is empty", input{subject: "", method: "list"}, expect{list: []string{}, compareLegacy: true}},
		{"internal reader can see foreign artifact", input{internal: true, id: "artifact-foreign"}, expect{id: "artifact-foreign"}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			_, oldPath := reportArtifactFixture(t, project)
			db, _ := reportArtifactFixture(t, project)
			rt, key, _ := reportArtifactRuntime(t, db, tc.input.subject, tc.input.internal, nil)
			query := &read.Input{}
			if tc.input.id != "" {
				query.SetArtifactID(tc.input.id)
			}
			value, err := rt.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "GET", Path: "/v1/internal/forge/reporting/artifact"}}, Input: query})
			must(t, err)
			rows := value.(*read.Output).Data
			if tc.expect.list != nil {
				ids := []string{}
				for _, row := range rows {
					ids = append(ids, row.ArtifactId)
				}
				if !reflect.DeepEqual(ids, tc.expect.list) {
					t.Fatalf("ids=%v expected=%v", ids, tc.expect.list)
				}
			} else if tc.expect.id == "" {
				if len(rows) != 0 {
					t.Fatalf("foreign artifact leaked: %+v", rows)
				}
			} else if len(rows) != 1 || rows[0].ArtifactId != tc.expect.id {
				t.Fatalf("rows=%+v expected=%q", rows, tc.expect.id)
			}
			if !tc.expect.compareLegacy {
				return
			}
			body, e := json.Marshal(map[string]any{"artifactId": tc.input.id})
			must(t, e)
			payload, e := json.Marshal(map[string]any{"Component": "reportArtifact", "DBPath": oldPath, "Principal": tc.input.subject, "Method": tc.input.method, "Body": string(body)})
			must(t, e)
			cmd := exec.Command(legacy)
			cmd.Stdin = bytes.NewReader(payload)
			raw, e := cmd.Output()
			must(t, e)
			var before probeResult
			must(t, json.Unmarshal(raw, &before))
			if before.Failed != (tc.expect.id == "" && tc.expect.list == nil) {
				t.Fatalf("legacy failure=%v (%s), native rows=%d", before.Failed, before.Error, len(rows))
			}
			if tc.expect.id == "" && tc.expect.list == nil {
				return
			}
			if tc.expect.list != nil {
				var oldRows []reportArtifactSnapshot
				must(t, json.Unmarshal(before.Output, &oldRows))
				ids := []string{}
				for _, row := range oldRows {
					ids = append(ids, row.ArtifactID)
				}
				if !reflect.DeepEqual(ids, tc.expect.list) {
					t.Fatalf("legacy ids=%v native=%v", ids, tc.expect.list)
				}
				return
			}
			nativeJSON, e := json.Marshal(reportArtifactSnapshotFromView(rows[0]))
			must(t, e)
			var oldFields, newFields map[string]any
			must(t, json.Unmarshal(before.Output, &oldFields))
			must(t, json.Unmarshal(nativeJSON, &newFields))
			if !reflect.DeepEqual(oldFields, newFields) {
				t.Fatalf("legacy=%s native=%s", before.Output, nativeJSON)
			}
		})
	}
}

func TestReportArtifactDeleteJobGuard(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type input struct {
		id, expectedJob, subject string
	}
	type expect struct {
		failure   bool
		remaining int
	}
	type useCase struct {
		desc   string
		input  input
		expect expect
	}
	for _, tc := range []useCase{
		{"matching job deletes artifact", input{"artifact-running", "running", "u1"}, expect{}},
		{"wrong job guard rejects deletion", input{"artifact-running", "queued", "u1"}, expect{failure: true, remaining: 1}},
		{"foreign owner rejects deletion", input{"artifact-running", "running", "u2"}, expect{failure: true, remaining: 1}},
		{"missing artifact is strict", input{"missing", "running", "u1"}, expect{failure: true}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			db, _ := reportArtifactFixture(t, project)
			rt, _, key := reportArtifactRuntime(t, db, tc.input.subject, false, nil)
			row := &write.Artifact{}
			row.SetArtifactId(tc.input.id)
			row.SetShouldDelete(true)
			input := &write.Input{}
			input.SetMode("delete")
			input.SetExpectedJobID(tc.input.expectedJob)
			input.SetArtifacts([]*write.Artifact{row})
			_, err := invokeReportArtifactWriter(context.Background(), rt, key, input)
			if (err != nil) != tc.expect.failure {
				t.Fatalf("delete error=%v expected failure=%v", err, tc.expect.failure)
			}
			var remaining int
			must(t, db.QueryRow("SELECT COUNT(*) FROM report_export_artifact WHERE artifact_id=?", tc.input.id).Scan(&remaining))
			if remaining != tc.expect.remaining {
				t.Fatalf("remaining=%d expected=%d", remaining, tc.expect.remaining)
			}
		})
	}
}

func TestReportArtifactCallerTransaction(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type useCase struct {
		desc   string
		commit bool
		expect int
	}
	for _, tc := range []useCase{{"caller rollback", false, 0}, {"caller commit", true, 1}} {
		t.Run(tc.desc, func(t *testing.T) {
			db, _ := reportArtifactFixture(t, project)
			db.SetMaxOpenConns(1)
			tx, err := db.BeginTx(context.Background(), nil)
			must(t, err)
			defer tx.Rollback()
			rt, _, key := reportArtifactRuntime(t, db, "u1", false, tx)
			input := &write.Input{}
			input.SetMode("create")
			input.SetArtifacts([]*write.Artifact{reportArtifactNew("new", "queued", "report://queued", "pdf", 60)})
			_, err = invokeReportArtifactWriter(context.Background(), rt, key, input)
			must(t, err)
			var pending int
			must(t, tx.QueryRow("SELECT COUNT(*) FROM report_export_artifact WHERE artifact_id='new'").Scan(&pending))
			if pending != 1 {
				t.Fatalf("pending count=%d", pending)
			}
			if tc.commit {
				must(t, tx.Commit())
			} else {
				must(t, tx.Rollback())
			}
			var stored int
			must(t, db.QueryRow("SELECT COUNT(*) FROM report_export_artifact WHERE artifact_id='new'").Scan(&stored))
			if stored != tc.expect {
				t.Fatalf("stored count=%d expected=%d", stored, tc.expect)
			}
		})
	}
}

func TestReportArtifactOutputOwnsBytes(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	db, _ := reportArtifactFixture(t, project)
	rt, _, key := reportArtifactRuntime(t, db, "u1", false, nil)
	row := reportArtifactNew("new", "queued", "report://queued", "pdf", 60)
	input := &write.Input{}
	input.SetMode("create")
	input.SetArtifacts([]*write.Artifact{row})
	out, err := invokeReportArtifactWriter(context.Background(), rt, key, input)
	must(t, err)
	if len(out.Data) != 1 || string(out.Data[0].InlineData) != "%PDF" {
		t.Fatalf("output=%+v", out.Data)
	}
	out.Data[0].InlineData[0] = 'X'
	if string(row.InlineData) != "%PDF" {
		t.Fatal("writer output aliases mutable input bytes")
	}
}

func TestReportArtifactHTTPCannotOverrideOwner(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	db, _ := reportArtifactFixture(t, project)
	rt, _, _ := reportArtifactRuntime(t, db, "u2", false, nil)
	request := httptest.NewRequest("GET", "/v1/internal/forge/reporting/artifact?artifactId=artifact-running&internal=true&ownerSubject=u1", nil)
	scope, err := requestprovider.New(request)
	must(t, err)
	defer scope.Close()
	value, err := rt.ExecuteRoute(context.Background(), "GET", "/v1/internal/forge/reporting/artifact", scope)
	must(t, err)
	if len(value.(*read.Output).Data) != 0 {
		t.Fatal("HTTP query replaced host artifact owner")
	}
	mutation := httptest.NewRequest("PATCH", "/v1/internal/forge/reporting/artifact?mode=create&internal=true&ownerSubject=u1", bytes.NewBufferString(`{"data":[{"artifactId":"new","jobId":"queued","artifactRef":"report://queued","ownerId":"u1","format":"pdf","contentType":"application/pdf","createdAt":"2026-01-04T00:00:00Z"}]}`))
	mutation.Header.Set("Content-Type", "application/json")
	writeScope, err := requestprovider.New(mutation)
	must(t, err)
	defer writeScope.Close()
	if _, err := rt.ExecuteRoute(context.Background(), "PATCH", "/v1/internal/forge/reporting/artifact", writeScope); err == nil {
		t.Fatal("HTTP query replaced host artifact writer owner")
	}
	var count int
	must(t, db.QueryRow("SELECT COUNT(*) FROM report_export_artifact WHERE artifact_id='new'").Scan(&count))
	if count != 0 {
		t.Fatalf("untrusted request created artifact")
	}
}
