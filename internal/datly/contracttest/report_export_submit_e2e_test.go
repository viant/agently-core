package tests

import (
	"context"
	"database/sql"
	"errors"
	"github.com/viant/datly/runtime/handler/provider"
	"path/filepath"
	"reflect"
	"runtime"
	"sync"
	"testing"
	"time"

	sqlite3 "github.com/mattn/go-sqlite3"
	jobread "github.com/viant/agently-core/internal/datly/reporting/job/read"
	jobwrite "github.com/viant/agently-core/internal/datly/reporting/job/write"
	runread "github.com/viant/agently-core/internal/datly/reporting/run/read"
	submit "github.com/viant/agently-core/internal/store/reporting/exportsubmit"
	"github.com/viant/bindly/locator"
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
	dtag "github.com/viant/datly/tag"
)

func reportExportSubmitFixture(t *testing.T, project string) (*sql.DB, string) {
	t.Helper()
	db, path := reportJobFixture(t, project)
	_, err := db.Exec(`INSERT INTO report_run(report_run_id,owner_id,conversation_id,materializer,origin,status,started_at,completed_at,revision,ui_run_request_id,report_spec_json,report_fill_json,report_print_json,created_at,updated_at) VALUES
 ('manual','u1','c1','test','manual','completed','2026-01-01 00:00:00','2026-01-02 00:00:00',2,'req-manual',X'7B7D',X'7B7D',X'7B7D','2026-01-01 00:00:00','2026-01-02 00:00:00'),
 ('pending','u1','c1','test','manual','running','2026-01-01 00:00:00',NULL,1,'req-pending',X'7B7D',X'7B7D',X'7B7D','2026-01-01 00:00:00','2026-01-02 00:00:00'),
 ('other','u1','c1','test','manual','completed','2026-01-01 00:00:00','2026-01-02 00:00:00',2,'req-other',X'7B7D',X'7B7D',X'7B7D','2026-01-01 00:00:00','2026-01-02 00:00:00')`)
	must(t, err)
	return db, path
}

func reportExportSubmitRuntime(t *testing.T, db *sql.DB, owner string, supplied *sql.Tx) (*druntime.Runtime, spec.Key) {
	t.Helper()
	resources := resource.New()
	must(t, resources.Register(jobread.ReaderDatlyResourceNamespace, jobread.ReaderDatlyResources))
	must(t, resources.Register(jobwrite.WriterDatlyResourceNamespace, jobwrite.WriterDatlyResources))
	must(t, resources.Register(runread.ReaderDatlyResourceNamespace, runread.ReaderDatlyResources))
	jr := payloadArtifact(t, resources, reflect.TypeFor[jobread.ReaderComponent](), reflect.TypeFor[jobread.Input](), reflect.TypeFor[jobread.Output]())
	jw := payloadArtifact(t, resources, reflect.TypeFor[jobwrite.WriterComponent](), reflect.TypeFor[jobwrite.Input](), reflect.TypeFor[jobwrite.Output]())
	rr := payloadArtifact(t, resources, reflect.TypeFor[runread.ReaderComponent](), reflect.TypeFor[runread.Input](), reflect.TypeFor[runread.Output]())
	parent := payloadArtifact(t, resources, reflect.TypeFor[submit.Component](), reflect.TypeFor[submit.Input](), reflect.TypeFor[submit.Output]())
	customHandler, err := (submit.Component{}).DatlyHandler("NewSubmit")()
	must(t, err)
	builder, err := bootstrap.NewArtifactBuilder(nil)
	must(t, err)
	parent, err = builder.Build(bootstrap.ArtifactInput{Component: parent.Component, InputType: reflect.TypeFor[submit.Input](), OutputType: reflect.TypeFor[submit.Output](), Resources: resources, Handler: customHandler})
	must(t, err)
	visibility := provider.Named("visibility", func(context.Context, reflect.Type, string) (any, bool, error) { return &owner, true, nil })
	access := ordinaryAccess("reportaccess", func(context.Context, reflect.Type, string) (any, bool, error) { return false, true, nil })
	parentReg, err := parent.Registration(registry.RegisteredComponent{DataSource: dml.Source{DB: db, Tx: supplied}, Providers: []locator.Provider{visibility, access}})
	must(t, err)
	jobReader, err := jr.ReaderCompilation().NewExecution(bootstrap.ReaderRuntimeConfig{SQL: &dsql.SQLComponent{DB: db}})
	must(t, err)
	runReader, err := rr.ReaderCompilation().NewExecution(bootstrap.ReaderRuntimeConfig{SQL: &dsql.SQLComponent{DB: db}})
	must(t, err)
	views, err := viewprovider.New(viewprovider.Config{Dependencies: jw.ViewDependencies, Input: jw.Input, SQL: &dsql.SQLComponent{DB: db}})
	must(t, err)
	handler, err := writer.New(jw.Component, reflect.TypeFor[jobwrite.Input](), reflect.TypeFor[jobwrite.Output](), "patch")
	must(t, err)
	rt, err := druntime.NewRuntime([]*registry.RegisteredComponent{
		{Component: jr.Component, Input: jr.Input, Output: jr.Output, OutputType: reflect.TypeFor[jobread.Output](), Reader: jobReader, Providers: []locator.Provider{visibility, access}},
		{Component: rr.Component, Input: rr.Input, Output: rr.Output, OutputType: reflect.TypeFor[runread.Output](), Reader: runReader, Providers: []locator.Provider{visibility, access}},
		{Component: jw.Component, Input: jw.Input, Output: jw.Output, OutputType: reflect.TypeFor[jobwrite.Output](), Handler: handler, Providers: []locator.Provider{visibility, access, views}, DataSource: dml.Source{DB: db}},
		parentReg,
	}, druntime.WithResources(resources))
	must(t, err)
	return rt, parent.Component.Key
}

func reportExportCandidate() *submit.Input {
	return &submit.Input{
		JobID: "export", OwnerID: "u1", ConversationID: "c1", ReportRunID: "manual",
		ExportRequestID: "export-one", ArtifactRef: "report-run://manual", Format: "pdf", Scope: "draft", Status: "queued",
		SubmittedAt: time.Date(2026, 1, 4, 0, 0, 0, 0, time.UTC),
	}
}

func invokeReportExportSubmit(ctx context.Context, rt *druntime.Runtime, key spec.Key, input *submit.Input) (*submit.Output, error) {
	value, err := rt.InvokeComponent(ctx, dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "POST", Path: "/v1/internal/forge/reporting/export/submit"}}, Input: input})
	if err != nil {
		return nil, err
	}
	return value.(*submit.Output), nil
}

func TestReportExportSubmitNativeFlow(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type input struct {
		seedReplay, mismatchReplay, missingRun, wrongConversation, runningRun, emptySnapshot, malformed, foreignOwner, rejectInsert bool
	}
	type expect struct {
		failure bool
		class   error
		replay  bool
		count   int
	}
	type useCase struct {
		desc   string
		input  input
		expect expect
	}
	for _, tc := range []useCase{
		{"completed run submits copied snapshots", input{}, expect{count: 1}},
		{"identical request replays existing job", input{seedReplay: true}, expect{replay: true, count: 1}},
		{"different run under same request conflicts", input{seedReplay: true, mismatchReplay: true}, expect{failure: true, class: submit.ErrConflict, count: 1}},
		{"missing run is not found", input{missingRun: true}, expect{failure: true, class: submit.ErrNotFound}},
		{"run in another conversation is not found", input{wrongConversation: true}, expect{failure: true, class: submit.ErrNotFound}},
		{"incomplete run cannot submit", input{runningRun: true}, expect{failure: true, class: submit.ErrInvalidTransition}},
		{"missing snapshot blocks submit", input{emptySnapshot: true}, expect{failure: true, class: submit.ErrInvalidTransition}},
		{"malformed candidate rejected", input{malformed: true}, expect{failure: true, class: submit.ErrConflict}},
		{"foreign owner cannot submit", input{foreignOwner: true}, expect{failure: true, class: submit.ErrNotFound}},
		{"insert failure leaves no job", input{rejectInsert: true}, expect{failure: true}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			db, _ := reportExportSubmitFixture(t, project)
			if tc.input.seedReplay {
				for _, fixture := range []*sql.DB{db} {
					_, err := fixture.Exec(`INSERT INTO report_export_job(job_id,artifact_ref,owner_id,conversation_id,format,scope,status,report_run_id,report_run_revision,export_request_id,report_spec_json,report_fill_json,report_print_json,submitted_at) VALUES('prior','report-run://manual','u1','c1','pdf','draft','queued','manual',2,'export-one',X'7B7D',X'7B7D',X'7B7D','2026-01-03 00:00:00')`)
					must(t, err)
				}
			}
			if tc.input.emptySnapshot {
				for _, fixture := range []*sql.DB{db} {
					_, err := fixture.Exec("UPDATE report_run SET report_fill_json=NULL WHERE report_run_id='manual'")
					must(t, err)
				}
			}
			if tc.input.rejectInsert {
				for _, fixture := range []*sql.DB{db} {
					_, err := fixture.Exec("CREATE TRIGGER reject_export_submit BEFORE INSERT ON report_export_job WHEN NEW.job_id='export' BEGIN SELECT RAISE(ABORT,'fixture submit rejection'); END")
					must(t, err)
				}
			}
			owner := "u1"
			if tc.input.foreignOwner {
				owner = "u2"
			}
			rt, key := reportExportSubmitRuntime(t, db, owner, nil)
			candidate := reportExportCandidate()
			if tc.input.mismatchReplay {
				candidate.ReportRunID, candidate.ArtifactRef = "other", "report-run://other"
			}
			if tc.input.missingRun {
				candidate.ReportRunID, candidate.ArtifactRef = "missing", "report-run://missing"
			}
			if tc.input.wrongConversation {
				candidate.ConversationID = "c2"
			}
			if tc.input.runningRun {
				candidate.ReportRunID, candidate.ArtifactRef = "pending", "report-run://pending"
			}
			if tc.input.malformed {
				candidate.Format = "csv"
			}
			output, err := invokeReportExportSubmit(context.Background(), rt, key, candidate)
			if (err != nil) != tc.expect.failure {
				t.Fatalf("submit result=%+v err=%v expected failure=%v", output, err, tc.expect.failure)
			}
			if tc.expect.class != nil && !errors.Is(err, tc.expect.class) {
				t.Fatalf("submit error=%v want %v", err, tc.expect.class)
			}
			if err == nil {
				if output.Replay != tc.expect.replay || output.Job == nil {
					t.Fatalf("submit output=%+v", output)
				}
				if !output.Replay && (output.Job.ReportRunId != candidate.ReportRunID || output.Job.ReportRunRevision != 2 || string(output.Job.ReportSpecJson) != "{}" || string(output.Job.ReportFillJson) != "{}" || string(output.Job.ReportPrintJson) != "{}") {
					t.Fatalf("copied snapshot=%+v", output.Job)
				}
			}
			var count int
			must(t, db.QueryRow("SELECT COUNT(*) FROM report_export_job WHERE export_request_id='export-one'").Scan(&count))
			if count != tc.expect.count {
				t.Fatalf("job count=%d expected=%d", count, tc.expect.count)
			}
		})
	}
}

func TestReportExportSubmitIndependentConnections(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	db, path := reportExportSubmitFixture(t, project)
	_, err := db.Exec("PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000")
	must(t, err)
	other, err := sql.Open("sqlite3", path+"?_foreign_keys=on&_busy_timeout=5000")
	must(t, err)
	defer other.Close()
	other.SetMaxOpenConns(1)
	_, err = other.Exec("PRAGMA busy_timeout=5000")
	must(t, err)
	first, firstKey := reportExportSubmitRuntime(t, db, "u1", nil)
	second, secondKey := reportExportSubmitRuntime(t, other, "u1", nil)
	runtimes := []*druntime.Runtime{first, second}
	keys := []spec.Key{firstKey, secondKey}
	results := make([]*submit.Output, 2)
	errorsByCaller := make([]error, 2)
	start := make(chan struct{})
	var group sync.WaitGroup
	for i := range results {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			<-start
			results[index], errorsByCaller[index] = invokeReportExportSubmit(context.Background(), runtimes[index], keys[index], reportExportCandidate())
		}(i)
	}
	close(start)
	group.Wait()
	created, replayed := 0, 0
	for i, result := range results {
		if errorsByCaller[i] != nil {
			var sqliteError sqlite3.Error
			if !errors.As(errorsByCaller[i], &sqliteError) || sqliteError.Code != sqlite3.ErrBusy && sqliteError.Code != sqlite3.ErrLocked {
				t.Fatalf("caller %d unexpected contention result=%+v error=%v", i, result, errorsByCaller[i])
			}
			// The first invocation rolled back its read snapshot on SQLITE_BUSY.
			// A fresh invocation is an explicit caller retry, which must return
			// the winner rather than create a second job.
			result, errorsByCaller[i] = invokeReportExportSubmit(context.Background(), runtimes[i], keys[i], reportExportCandidate())
		}
		if errorsByCaller[i] != nil || result == nil || result.Job == nil {
			t.Fatalf("caller %d retry result=%+v error=%v", i, result, errorsByCaller[i])
		}
		if result.Replay {
			replayed++
		} else {
			created++
		}
	}
	if created != 1 || replayed != 1 {
		t.Fatalf("created=%d replayed=%d", created, replayed)
	}
	var count int
	must(t, db.QueryRow("SELECT COUNT(*) FROM report_export_job WHERE export_request_id='export-one'").Scan(&count))
	if count != 1 {
		t.Fatalf("persisted request count=%d", count)
	}
}

func TestReportExportSubmitCallerTransaction(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type useCase struct {
		desc   string
		commit bool
		expect int
	}
	for _, tc := range []useCase{{"caller rollback", false, 0}, {"caller commit", true, 1}} {
		t.Run(tc.desc, func(t *testing.T) {
			db, _ := reportExportSubmitFixture(t, project)
			db.SetMaxOpenConns(1)
			tx, err := db.BeginTx(context.Background(), nil)
			must(t, err)
			defer tx.Rollback()
			rt, key := reportExportSubmitRuntime(t, db, "u1", tx)
			out, err := invokeReportExportSubmit(context.Background(), rt, key, reportExportCandidate())
			must(t, err)
			if out == nil || out.Replay || out.Job == nil {
				t.Fatalf("pending output=%+v", out)
			}
			var pending int
			must(t, tx.QueryRow("SELECT COUNT(*) FROM report_export_job WHERE job_id='export'").Scan(&pending))
			if pending != 1 {
				t.Fatalf("pending count=%d", pending)
			}
			if tc.commit {
				must(t, tx.Commit())
			} else {
				must(t, tx.Rollback())
			}
			var stored int
			must(t, db.QueryRow("SELECT COUNT(*) FROM report_export_job WHERE job_id='export'").Scan(&stored))
			if stored != tc.expect {
				t.Fatalf("stored count=%d expected=%d", stored, tc.expect)
			}
		})
	}
}

func TestReportExportSubmitHostRoutePrivate(t *testing.T) {
	holder := reflect.TypeFor[submit.Component]()
	field, ok := holder.FieldByName("Contract")
	if !ok {
		t.Fatal("submit contract is missing")
	}
	tag, present, err := dtag.ParseComponent(field.Tag)
	must(t, err)
	if !present {
		t.Fatal("submit component tag is missing")
	}
	source := &bootstrap.RouteSource{HolderType: holder.Name(), FieldName: field.Name, PackageName: "exportsubmit", PackagePath: holder.PkgPath(), Tag: tag, InputType: "Input", OutputType: "Output"}
	component, err := source.Resolve(reflect.TypeFor[submit.Input](), reflect.TypeFor[submit.Output]())
	must(t, err)
	if len(component.Routes) != 1 || !component.Routes[0].Internal {
		t.Fatalf("submit route is public: %+v", component.Routes)
	}
}
