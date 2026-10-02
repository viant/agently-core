package tests

import (
	"bytes"
	"context"
	"database/sql"
	"github.com/viant/datly/runtime/handler/provider"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"runtime"
	"sync"
	"testing"
	"time"

	read "github.com/viant/agently-core/internal/datly/reporting/job/read"
	write "github.com/viant/agently-core/internal/datly/reporting/job/write"
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

func reportJobFixture(t *testing.T, project string) (*sql.DB, string) {
	t.Helper()
	db, path := goalFixture(t, project)
	_, err := db.Exec(`INSERT INTO report_export_job(job_id,artifact_ref,owner_id,format,scope,status,report_spec_json,report_fill_json,report_print_json,submitted_at,started_at,retention_ttl_sec) VALUES
 ('queued','report://queued','u1','pdf','draft','queued',X'7B7D',X'7B7D',X'7B7D','2026-01-01 00:00:00',NULL,0),
 ('running','report://running','u1','pdf','draft','running',X'7B7D',X'7B7D',X'7B7D','2026-01-02 00:00:00','2026-01-02 01:00:00',0),
 ('foreign','report://foreign','u2','pdf','draft','queued',X'7B7D',X'7B7D',X'7B7D','2026-01-03 00:00:00',NULL,0)`)
	must(t, err)
	return db, path
}

func reportJobRuntime(t *testing.T, db *sql.DB, subject string, internal bool, supplied *sql.Tx) (*druntime.Runtime, spec.Key, spec.Key) {
	t.Helper()
	resources := resource.New()
	must(t, resources.Register(read.ReaderDatlyResourceNamespace, read.ReaderDatlyResources))
	must(t, resources.Register(write.WriterDatlyResourceNamespace, write.WriterDatlyResources))
	r := payloadArtifact(t, resources, reflect.TypeFor[read.ReaderComponent](), reflect.TypeFor[read.Input](), reflect.TypeFor[read.Output]())
	w := payloadArtifact(t, resources, reflect.TypeFor[write.WriterComponent](), reflect.TypeFor[write.Input](), reflect.TypeFor[write.Output]())
	reader, err := r.ReaderCompilation().NewExecution(bootstrap.ReaderRuntimeConfig{SQL: &dsql.SQLComponent{DB: db}})
	must(t, err)
	views, err := viewprovider.New(viewprovider.Config{Dependencies: w.ViewDependencies, Input: w.Input, SQL: &dsql.SQLComponent{DB: db}})
	must(t, err)
	handler, err := writer.New(w.Component, reflect.TypeFor[write.Input](), reflect.TypeFor[write.Output](), "patch")
	must(t, err)
	access := ordinaryAccess("reportaccess", func(context.Context, reflect.Type, string) (any, bool, error) { return internal, true, nil })
	visibility := provider.Named("visibility", func(context.Context, reflect.Type, string) (any, bool, error) { return &subject, true, nil })
	rt, err := druntime.NewRuntime([]*registry.RegisteredComponent{
		{Component: r.Component, Input: r.Input, Output: r.Output, OutputType: reflect.TypeFor[read.Output](), Reader: reader, Providers: []locator.Provider{access, visibility}},
		{Component: w.Component, Input: w.Input, Output: w.Output, OutputType: reflect.TypeFor[write.Output](), Handler: handler, Providers: []locator.Provider{access, visibility, views}, DataSource: dml.Source{DB: db, Tx: supplied}},
	}, druntime.WithResources(resources))
	must(t, err)
	return rt, r.Component.Key, w.Component.Key
}

func invokeReportJobWriter(ctx context.Context, rt *druntime.Runtime, key spec.Key, input *write.Input) (*write.Output, error) {
	value, err := rt.InvokeComponent(ctx, dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "PATCH", Path: "/v1/internal/forge/reporting/job"}}, Input: input})
	if err != nil {
		return nil, err
	}
	return value.(*write.Output), nil
}

func reportJobNew(id string) *write.Job {
	row := &write.Job{}
	row.SetJobId(id)
	row.SetArtifactRef("report://" + id)
	row.SetOwnerId("u1")
	row.SetFormat("pdf")
	row.SetScope("draft")
	row.SetStatus("queued")
	row.SetReportSpecJson([]byte("{}"))
	row.SetReportFillJson([]byte("{}"))
	row.SetReportPrintJson([]byte("{}"))
	row.SetSubmittedAt(time.Date(2026, 1, 4, 0, 0, 0, 0, time.UTC))
	row.SetRetentionTtlSec(0)
	return row
}

func TestReportJobWriterModes(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type input struct {
		mode, id, owner string
		expectedStatus  string
	}
	type expect struct {
		failure bool
		status  string
	}
	type useCase struct {
		desc   string
		input  input
		expect expect
	}
	for _, tc := range []useCase{
		{"create unlinked job", input{mode: "create", id: "new", owner: "u1"}, expect{status: "queued"}},
		{"duplicate create fails", input{mode: "create", id: "queued", owner: "u1"}, expect{failure: true, status: "queued"}},
		{"claim queued job", input{mode: "claim", id: "queued", owner: "u1", expectedStatus: "queued"}, expect{status: "running"}},
		{"claim already running fails", input{mode: "claim", id: "running", owner: "u1", expectedStatus: "queued"}, expect{failure: true, status: "running"}},
		{"fail running job", input{mode: "fail", id: "running", owner: "u1", expectedStatus: "running"}, expect{status: "failed"}},
		{"fail queued job is invalid", input{mode: "fail", id: "queued", owner: "u1", expectedStatus: "running"}, expect{failure: true, status: "queued"}},
		{"wrong owner cannot claim", input{mode: "claim", id: "queued", owner: "u2", expectedStatus: "queued"}, expect{failure: true, status: "queued"}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			db, _ := reportJobFixture(t, project)
			rt, _, key := reportJobRuntime(t, db, tc.input.owner, false, nil)
			row := &write.Job{}
			switch tc.input.mode {
			case "create":
				row = reportJobNew(tc.input.id)
			case "claim":
				row.SetJobId(tc.input.id)
				row.SetStatus("running")
				started := time.Date(2026, 1, 4, 0, 0, 0, 0, time.UTC)
				row.SetStartedAt(&started)
			case "fail":
				row.SetJobId(tc.input.id)
				row.SetStatus("failed")
				text := "export failed"
				row.SetErrorText(&text)
				completed := time.Date(2026, 1, 4, 0, 0, 0, 0, time.UTC)
				row.SetCompletedAt(&completed)
			}
			input := &write.Input{}
			input.SetMode(tc.input.mode)
			input.SetJobs([]*write.Job{row})
			if tc.input.expectedStatus != "" {
				input.SetExpectedStatus(tc.input.expectedStatus)
			}
			_, err := invokeReportJobWriter(context.Background(), rt, key, input)
			if (err != nil) != tc.expect.failure {
				t.Fatalf("job writer error=%v expected failure=%v", err, tc.expect.failure)
			}
			var status string
			if err := db.QueryRow("SELECT status FROM report_export_job WHERE job_id=?", tc.input.id).Scan(&status); err != nil {
				t.Fatal(err)
			}
			if status != tc.expect.status {
				t.Fatalf("status=%q expected=%q", status, tc.expect.status)
			}
		})
	}
}

func reportJobReaderFixture(t *testing.T, project string) (*sql.DB, string) {
	t.Helper()
	db, path := reportJobFixture(t, project)
	_, err := db.Exec(`UPDATE report_export_job SET conversation_id='c1', workspace_id='workspace',
 auth_context_ref='auth', metadata_json=X'7B2261223A317D', artifact_id='artifact',
 error_text='error', diagnostics_json=X'7B2262223A327D', retention_ttl_sec=90
 WHERE job_id='queued';
 INSERT INTO report_run(report_run_id,owner_id,conversation_id,materializer,origin,status,started_at,completed_at,revision,ui_run_request_id,report_spec_json,report_fill_json,report_print_json,created_at,updated_at)
 VALUES('manual','u1','c2','test','manual','completed','2026-01-01 00:00:00','2026-01-02 00:00:00',2,'req-manual',X'7B7D',X'7B7D',X'7B7D','2026-01-01 00:00:00','2026-01-02 00:00:00');
 UPDATE report_export_job SET conversation_id='c2',report_run_id='manual',report_run_revision=2,export_request_id='export-one'
 WHERE job_id='running'`)
	must(t, err)
	return db, path
}

type reportJobSnapshot struct {
	JobID             string        `json:"jobId,omitempty"`
	ArtifactRef       string        `json:"artifactRef,omitempty"`
	OwnerID           string        `json:"ownerId,omitempty"`
	ConversationID    string        `json:"conversationId,omitempty"`
	WorkspaceID       string        `json:"workspaceId,omitempty"`
	AuthContextRef    string        `json:"authContextRef,omitempty"`
	Format            string        `json:"format,omitempty"`
	Scope             string        `json:"scope,omitempty"`
	Status            string        `json:"status,omitempty"`
	ReportRunID       string        `json:"reportRunId,omitempty"`
	ReportRunRevision int64         `json:"reportRunRevision,omitempty"`
	ExportRequestID   string        `json:"exportRequestId,omitempty"`
	ReportSpec        []byte        `json:"reportSpec,omitempty"`
	ReportFill        []byte        `json:"reportFill,omitempty"`
	ReportPrint       []byte        `json:"reportPrint,omitempty"`
	Metadata          []byte        `json:"metadata,omitempty"`
	ArtifactID        string        `json:"artifactId,omitempty"`
	Error             string        `json:"error,omitempty"`
	Diagnostics       []byte        `json:"diagnostics,omitempty"`
	SubmittedAt       time.Time     `json:"submittedAt,omitempty"`
	StartedAt         *time.Time    `json:"startedAt,omitempty"`
	CompletedAt       *time.Time    `json:"completedAt,omitempty"`
	RetentionTTL      time.Duration `json:"retentionTtl,omitempty"`
}

func reportJobSnapshotFromView(row *read.Job) reportJobSnapshot {
	return reportJobSnapshot{
		JobID: row.JobId, ArtifactRef: row.ArtifactRef, OwnerID: row.OwnerId,
		ConversationID: row.ConversationId, WorkspaceID: row.WorkspaceId, AuthContextRef: row.AuthContextRef,
		Format: row.Format, Scope: row.Scope, Status: row.Status,
		ReportRunID: row.ReportRunId, ReportRunRevision: row.ReportRunRevision, ExportRequestID: row.ExportRequestId,
		ReportSpec: row.ReportSpecJson, ReportFill: row.ReportFillJson, ReportPrint: row.ReportPrintJson,
		Metadata: row.MetadataJson, ArtifactID: row.ArtifactId, Error: row.ErrorText, Diagnostics: row.DiagnosticsJson,
		SubmittedAt: row.SubmittedAt, StartedAt: row.StartedAt, CompletedAt: row.CompletedAt,
		RetentionTTL: time.Duration(row.RetentionTtlSec) * time.Second,
	}
}

func TestReportJobReaderLegacyParity(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type input struct {
		subject, id, exportRequestID, method string
		internal                             bool
	}
	type expect struct {
		id   string
		list []string
	}
	type useCase struct {
		desc   string
		input  input
		expect expect
	}
	for _, tc := range []useCase{
		{"rich job includes all physical fields", input{subject: "u1", id: "queued", method: "get"}, expect{id: "queued"}},
		{"run-linked job includes T2 fields", input{subject: "u1", id: "running", method: "get"}, expect{id: "running"}},
		{"owner list uses submitted order", input{subject: "u1", method: "list"}, expect{list: []string{"running", "queued"}}},
		{"foreign job remains hidden", input{subject: "u2", id: "running", method: "get"}, expect{}},
		{"internal reader sees foreign job", input{internal: true, id: "foreign"}, expect{id: "foreign"}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			db, _ := reportJobReaderFixture(t, project)
			rt, key, _ := reportJobRuntime(t, db, tc.input.subject, tc.input.internal, nil)
			query := &read.Input{}
			if tc.input.id != "" {
				query.SetJobID(tc.input.id)
			}
			if tc.input.exportRequestID != "" {
				query.SetExportRequestID(tc.input.exportRequestID)
			}
			value, err := rt.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "GET", Path: "/v1/internal/forge/reporting/job"}}, Input: query})
			must(t, err)
			rows := value.(*read.Output).Data
			if tc.expect.list != nil {
				ids := []string{}
				for _, row := range rows {
					ids = append(ids, row.JobId)
				}
				if !reflect.DeepEqual(ids, tc.expect.list) {
					t.Fatalf("ids=%v expected=%v", ids, tc.expect.list)
				}
			} else if tc.expect.id == "" {
				if len(rows) != 0 {
					t.Fatalf("foreign job leaked: %+v", rows)
				}
			} else if len(rows) != 1 || rows[0].JobId != tc.expect.id {
				t.Fatalf("rows=%+v expected=%q", rows, tc.expect.id)
			}
			if tc.expect.id == "queued" {
				row := rows[0]
				if row.ArtifactRef != "report://queued" || row.OwnerId != "u1" || row.Format != "pdf" || row.Scope != "draft" || row.Status != "queued" || string(row.ReportSpecJson) != "{}" || string(row.ReportFillJson) != "{}" || string(row.ReportPrintJson) != "{}" {
					t.Fatalf("queued job snapshot differs from fixture: %+v", row)
				}
			}
			if tc.expect.id == "running" {
				row := rows[0]
				if row.ArtifactRef != "report://running" || row.OwnerId != "u1" || row.Status != "running" || row.StartedAt == nil || !row.StartedAt.Equal(time.Date(2026, 1, 2, 1, 0, 0, 0, time.UTC)) {
					t.Fatalf("running job snapshot differs from fixture: %+v", row)
				}
			}
		})
	}
}

func TestReportJobNativeTransitionGuards(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type input struct {
		mode, id, subject, expectedStatus string
		linkRun                           bool
		malformed                         bool
	}
	type expect struct {
		failure bool
		status  string
		present bool
	}
	type useCase struct {
		desc   string
		input  input
		expect expect
	}
	for _, tc := range []useCase{
		{"update unlinked job", input{mode: "update", id: "queued", subject: "u1"}, expect{status: "failed", present: true}},
		{"complete running job", input{mode: "complete", id: "running", subject: "u1", expectedStatus: "running"}, expect{status: "succeeded", present: true}},
		{"complete queued job rejected", input{mode: "complete", id: "queued", subject: "u1", expectedStatus: "running"}, expect{failure: true, status: "queued", present: true}},
		{"owned strict deletion", input{mode: "delete", id: "queued", subject: "u1"}, expect{}},
		{"foreign deletion rejected", input{mode: "delete", id: "queued", subject: "u2"}, expect{failure: true, status: "queued", present: true}},
		{"unknown strict deletion rejected", input{mode: "delete", id: "missing", subject: "u1"}, expect{failure: true}},
		{"run-linked generic update rejected", input{mode: "update", id: "running", subject: "u1", linkRun: true}, expect{failure: true, status: "running", present: true}},
		{"submit linked export job", input{mode: "submit", id: "export", subject: "u1", linkRun: true}, expect{status: "queued", present: true}},
		{"malformed linked submit rejected", input{mode: "submit", id: "export", subject: "u1", linkRun: true, malformed: true}, expect{failure: true}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			db, _ := reportJobFixture(t, project)
			if tc.input.linkRun {
				_, err := db.Exec(`INSERT INTO report_run(report_run_id,owner_id,conversation_id,materializer,origin,status,started_at,completed_at,revision,ui_run_request_id,report_spec_json,report_fill_json,report_print_json,created_at,updated_at)
 VALUES('manual','u1','c1','test','manual','completed','2026-01-01 00:00:00','2026-01-02 00:00:00',2,'req-manual',X'7B7D',X'7B7D',X'7B7D','2026-01-01 00:00:00','2026-01-02 00:00:00')`)
				must(t, err)
				if tc.input.mode == "update" {
					_, err = db.Exec("UPDATE report_export_job SET conversation_id='c1',report_run_id='manual',report_run_revision=2,export_request_id='request-one' WHERE job_id='running'")
					must(t, err)
				}
			}
			rt, _, key := reportJobRuntime(t, db, tc.input.subject, false, nil)
			row := &write.Job{}
			row.SetJobId(tc.input.id)
			switch tc.input.mode {
			case "update":
				row.SetStatus("failed")
			case "complete":
				row.SetStatus("succeeded")
				artifact := "artifact-one"
				row.SetArtifactId(&artifact)
				completed := time.Date(2026, 1, 4, 0, 0, 0, 0, time.UTC)
				row.SetCompletedAt(&completed)
				row.SetDiagnosticsJson([]byte("{}"))
			case "delete":
				row.SetShouldDelete(true)
			case "submit":
				row = reportJobNew(tc.input.id)
				row.SetArtifactRef("report-run://manual")
				conversation, runID, requestID := "c1", "manual", "export-one"
				revision := int64(2)
				row.SetConversationId(&conversation)
				row.SetReportRunId(&runID)
				row.SetReportRunRevision(&revision)
				row.SetExportRequestId(&requestID)
				if tc.input.malformed {
					row.SetFormat("csv")
				}
			}
			input := &write.Input{}
			input.SetMode(tc.input.mode)
			input.SetJobs([]*write.Job{row})
			if tc.input.expectedStatus != "" {
				input.SetExpectedStatus(tc.input.expectedStatus)
			}
			_, err := invokeReportJobWriter(context.Background(), rt, key, input)
			if (err != nil) != tc.expect.failure {
				t.Fatalf("mode=%s error=%v expected failure=%v", tc.input.mode, err, tc.expect.failure)
			}
			var status string
			err = db.QueryRow("SELECT status FROM report_export_job WHERE job_id=?", tc.input.id).Scan(&status)
			if !tc.expect.present {
				if err != sql.ErrNoRows {
					t.Fatalf("expected no job, got status=%q err=%v", status, err)
				}
				return
			}
			must(t, err)
			if status != tc.expect.status {
				t.Fatalf("status=%q expected=%q", status, tc.expect.status)
			}
		})
	}
}

func TestReportJobClaimIndependentConnections(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	db, path := reportJobFixture(t, project)
	_, err := db.Exec("PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000")
	must(t, err)
	other, err := sql.Open("sqlite3", path+"?_foreign_keys=on&_busy_timeout=5000")
	must(t, err)
	defer other.Close()
	other.SetMaxOpenConns(1)
	_, err = other.Exec("PRAGMA busy_timeout=5000")
	must(t, err)
	first, _, firstKey := reportJobRuntime(t, db, "u1", false, nil)
	second, _, secondKey := reportJobRuntime(t, other, "u1", false, nil)
	runtimes := []*druntime.Runtime{first, second}
	keys := []spec.Key{firstKey, secondKey}
	start := make(chan struct{})
	results := make([]error, 2)
	var group sync.WaitGroup
	for i := range results {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			<-start
			row := &write.Job{}
			row.SetJobId("queued")
			row.SetStatus("running")
			started := time.Date(2026, 1, 4, 0, 0, 0, 0, time.UTC)
			row.SetStartedAt(&started)
			input := &write.Input{}
			input.SetMode("claim")
			input.SetExpectedStatus("queued")
			input.SetJobs([]*write.Job{row})
			_, results[index] = invokeReportJobWriter(context.Background(), runtimes[index], keys[index], input)
		}(i)
	}
	close(start)
	group.Wait()
	success := 0
	for _, result := range results {
		if result == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("claim successes=%d errors=%v", success, results)
	}
	var status string
	must(t, db.QueryRow("SELECT status FROM report_export_job WHERE job_id='queued'").Scan(&status))
	if status != "running" {
		t.Fatalf("final status=%q", status)
	}
}

func TestReportJobCallerTransaction(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type useCase struct {
		desc   string
		commit bool
		expect string
	}
	for _, tc := range []useCase{{"caller rollback", false, "queued"}, {"caller commit", true, "running"}} {
		t.Run(tc.desc, func(t *testing.T) {
			db, _ := reportJobFixture(t, project)
			db.SetMaxOpenConns(1)
			tx, err := db.BeginTx(context.Background(), nil)
			must(t, err)
			defer tx.Rollback()
			rt, _, key := reportJobRuntime(t, db, "u1", false, tx)
			row := &write.Job{}
			row.SetJobId("queued")
			row.SetStatus("running")
			started := time.Date(2026, 1, 4, 0, 0, 0, 0, time.UTC)
			row.SetStartedAt(&started)
			input := &write.Input{}
			input.SetMode("claim")
			input.SetExpectedStatus("queued")
			input.SetJobs([]*write.Job{row})
			_, err = invokeReportJobWriter(context.Background(), rt, key, input)
			must(t, err)
			var pending string
			must(t, tx.QueryRow("SELECT status FROM report_export_job WHERE job_id='queued'").Scan(&pending))
			if pending != "running" {
				t.Fatalf("pending status=%q", pending)
			}
			if tc.commit {
				must(t, tx.Commit())
			} else {
				must(t, tx.Rollback())
			}
			var stored string
			must(t, db.QueryRow("SELECT status FROM report_export_job WHERE job_id='queued'").Scan(&stored))
			if stored != tc.expect {
				t.Fatalf("stored status=%q expected=%q", stored, tc.expect)
			}
		})
	}
}

func TestReportJobOutputOwnsBytes(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	db, _ := reportJobFixture(t, project)
	rt, _, key := reportJobRuntime(t, db, "u1", false, nil)
	row := reportJobNew("new")
	input := &write.Input{}
	input.SetMode("create")
	input.SetJobs([]*write.Job{row})
	out, err := invokeReportJobWriter(context.Background(), rt, key, input)
	must(t, err)
	if len(out.Data) != 1 || string(out.Data[0].ReportSpecJson) != "{}" {
		t.Fatalf("output=%+v", out.Data)
	}
	out.Data[0].ReportSpecJson[0] = 'X'
	if string(row.ReportSpecJson) != "{}" {
		t.Fatal("writer output aliases mutable input bytes")
	}
}

func TestReportJobHTTPCannotOverrideOwner(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	db, _ := reportJobFixture(t, project)
	rt, _, _ := reportJobRuntime(t, db, "u2", false, nil)
	request := httptest.NewRequest("GET", "/v1/internal/forge/reporting/job?jobId=queued&internal=true&ownerSubject=u1", nil)
	scope, err := requestprovider.New(request)
	must(t, err)
	defer scope.Close()
	value, err := rt.ExecuteRoute(context.Background(), "GET", "/v1/internal/forge/reporting/job", scope)
	must(t, err)
	if len(value.(*read.Output).Data) != 0 {
		t.Fatal("HTTP query replaced host owner scope")
	}
	mutation := httptest.NewRequest("PATCH", "/v1/internal/forge/reporting/job?mode=claim&expectedStatus=queued&internal=true&ownerSubject=u1", bytes.NewBufferString(`{"data":[{"jobId":"queued","status":"running","startedAt":"2026-01-04T00:00:00Z"}]}`))
	mutation.Header.Set("Content-Type", "application/json")
	writeScope, err := requestprovider.New(mutation)
	must(t, err)
	defer writeScope.Close()
	if _, err := rt.ExecuteRoute(context.Background(), "PATCH", "/v1/internal/forge/reporting/job", writeScope); err == nil {
		t.Fatal("HTTP query replaced host writer owner")
	}
	var status string
	must(t, db.QueryRow("SELECT status FROM report_export_job WHERE job_id='queued'").Scan(&status))
	if status != "queued" {
		t.Fatalf("untrusted request changed job status=%q", status)
	}
}
