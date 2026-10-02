package tests

import (
	"context"
	"database/sql"
	"github.com/viant/datly/runtime/handler/provider"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	artifactread "github.com/viant/agently-core/internal/datly/reporting/artifact/read"
	artifactwrite "github.com/viant/agently-core/internal/datly/reporting/artifact/write"
	jobread "github.com/viant/agently-core/internal/datly/reporting/job/read"
	jobwrite "github.com/viant/agently-core/internal/datly/reporting/job/write"
	complete "github.com/viant/agently-core/internal/store/reporting/exportcomplete"
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

func reportExportCompleteRuntime(t *testing.T, db *sql.DB, owner string, supplied *sql.Tx) (*druntime.Runtime, spec.Key) {
	t.Helper()
	resources := resource.New()
	must(t, resources.Register(artifactread.ReaderDatlyResourceNamespace, artifactread.ReaderDatlyResources))
	must(t, resources.Register(artifactwrite.WriterDatlyResourceNamespace, artifactwrite.WriterDatlyResources))
	must(t, resources.Register(jobread.ReaderDatlyResourceNamespace, jobread.ReaderDatlyResources))
	must(t, resources.Register(jobwrite.WriterDatlyResourceNamespace, jobwrite.WriterDatlyResources))
	ar := payloadArtifact(t, resources, reflect.TypeFor[artifactread.ReaderComponent](), reflect.TypeFor[artifactread.Input](), reflect.TypeFor[artifactread.Output]())
	aw := payloadArtifact(t, resources, reflect.TypeFor[artifactwrite.WriterComponent](), reflect.TypeFor[artifactwrite.Input](), reflect.TypeFor[artifactwrite.Output]())
	jr := payloadArtifact(t, resources, reflect.TypeFor[jobread.ReaderComponent](), reflect.TypeFor[jobread.Input](), reflect.TypeFor[jobread.Output]())
	jw := payloadArtifact(t, resources, reflect.TypeFor[jobwrite.WriterComponent](), reflect.TypeFor[jobwrite.Input](), reflect.TypeFor[jobwrite.Output]())
	parent := payloadArtifact(t, resources, reflect.TypeFor[complete.Component](), reflect.TypeFor[complete.Input](), reflect.TypeFor[complete.Output]())
	handler, err := (complete.Component{}).DatlyHandler("NewComplete")()
	must(t, err)
	builder, err := bootstrap.NewArtifactBuilder(nil)
	must(t, err)
	parent, err = builder.Build(bootstrap.ArtifactInput{Component: parent.Component, InputType: reflect.TypeFor[complete.Input](), OutputType: reflect.TypeFor[complete.Output](), Resources: resources, Handler: handler})
	must(t, err)
	visibility := provider.Named("visibility", func(context.Context, reflect.Type, string) (any, bool, error) { return &owner, true, nil })
	access := ordinaryAccess("reportaccess", func(context.Context, reflect.Type, string) (any, bool, error) { return false, true, nil })
	parentReg, err := parent.Registration(registry.RegisteredComponent{DataSource: dml.Source{DB: db, Tx: supplied}, Providers: []locator.Provider{visibility, access}})
	must(t, err)
	artifactReader, err := ar.ReaderCompilation().NewExecution(bootstrap.ReaderRuntimeConfig{SQL: &dsql.SQLComponent{DB: db}})
	must(t, err)
	jobReader, err := jr.ReaderCompilation().NewExecution(bootstrap.ReaderRuntimeConfig{SQL: &dsql.SQLComponent{DB: db}})
	must(t, err)
	artifactViews, err := viewprovider.New(viewprovider.Config{Dependencies: aw.ViewDependencies, Input: aw.Input, SQL: &dsql.SQLComponent{DB: db}})
	must(t, err)
	jobViews, err := viewprovider.New(viewprovider.Config{Dependencies: jw.ViewDependencies, Input: jw.Input, SQL: &dsql.SQLComponent{DB: db}})
	must(t, err)
	artifactWriter, err := writer.New(aw.Component, reflect.TypeFor[artifactwrite.Input](), reflect.TypeFor[artifactwrite.Output](), "patch")
	must(t, err)
	jobWriter, err := writer.New(jw.Component, reflect.TypeFor[jobwrite.Input](), reflect.TypeFor[jobwrite.Output](), "patch")
	must(t, err)
	rt, err := druntime.NewRuntime([]*registry.RegisteredComponent{
		{Component: ar.Component, Input: ar.Input, Output: ar.Output, OutputType: reflect.TypeFor[artifactread.Output](), Reader: artifactReader, Providers: []locator.Provider{visibility, access}},
		{Component: jr.Component, Input: jr.Input, Output: jr.Output, OutputType: reflect.TypeFor[jobread.Output](), Reader: jobReader, Providers: []locator.Provider{visibility, access}},
		{Component: aw.Component, Input: aw.Input, Output: aw.Output, OutputType: reflect.TypeFor[artifactwrite.Output](), Handler: artifactWriter, Providers: []locator.Provider{visibility, access, artifactViews}, DataSource: dml.Source{DB: db}},
		{Component: jw.Component, Input: jw.Input, Output: jw.Output, OutputType: reflect.TypeFor[jobwrite.Output](), Handler: jobWriter, Providers: []locator.Provider{visibility, access, jobViews}, DataSource: dml.Source{DB: db}},
		parentReg,
	}, druntime.WithResources(resources))
	must(t, err)
	return rt, parent.Component.Key
}

func reportExportCompleteInput() *complete.Input {
	return &complete.Input{JobID: "running", ArtifactID: "new-artifact", ContentType: "application/pdf", Data: []byte("%PDF-new"),
		ArtifactCreatedAt: time.Date(2026, 1, 4, 0, 0, 0, 0, time.UTC), Diagnostics: []byte(`{"ok":true}`),
		CompletedAt: time.Date(2026, 1, 4, 1, 0, 0, 0, time.UTC), RetentionTTL: 90 * time.Second}
}

func invokeReportExportComplete(ctx context.Context, rt *druntime.Runtime, key spec.Key, input *complete.Input) (*complete.Output, error) {
	value, err := rt.InvokeComponent(ctx, dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "POST", Path: "/v1/internal/forge/reporting/export/complete"}}, Input: input})
	if err != nil {
		return nil, err
	}
	return value.(*complete.Output), nil
}

func TestReportExportCompleteNativeFlow(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type input struct{ existingArtifact, replay, queued, wrongOwner, rejectJob bool }
	type expect struct {
		failure   bool
		status    string
		artifacts int
	}
	type useCase struct {
		desc   string
		input  input
		expect expect
	}
	for _, tc := range []useCase{
		{"new artifact and job finish together", input{}, expect{status: "succeeded", artifacts: 1}},
		{"existing matching artifact is reused", input{existingArtifact: true}, expect{status: "succeeded", artifacts: 1}},
		{"completed job replays matching artifact", input{existingArtifact: true, replay: true}, expect{status: "succeeded", artifacts: 1}},
		{"queued job cannot complete", input{queued: true}, expect{failure: true, status: "queued"}},
		{"foreign owner cannot complete", input{wrongOwner: true}, expect{failure: true, status: "running"}},
		{"late job failure rolls back inserted artifact", input{rejectJob: true}, expect{failure: true, status: "running"}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			db, _ := reportArtifactFixture(t, project)
			if !tc.input.existingArtifact {
				for _, fixture := range []*sql.DB{db} {
					_, err := fixture.Exec("DELETE FROM report_export_artifact WHERE job_id='running'")
					must(t, err)
				}
			}
			if tc.input.replay {
				for _, fixture := range []*sql.DB{db} {
					_, err := fixture.Exec("UPDATE report_export_job SET status='succeeded',artifact_id='artifact-running' WHERE job_id='running'")
					must(t, err)
				}
			}
			if tc.input.rejectJob {
				for _, fixture := range []*sql.DB{db} {
					_, err := fixture.Exec("CREATE TRIGGER reject_job_complete BEFORE UPDATE ON report_export_job WHEN OLD.job_id='running' AND NEW.status='succeeded' BEGIN SELECT RAISE(ABORT,'fixture job completion rejection'); END")
					must(t, err)
				}
			}
			owner := "u1"
			if tc.input.wrongOwner {
				owner = "u2"
			}
			rt, key := reportExportCompleteRuntime(t, db, owner, nil)
			candidate := reportExportCompleteInput()
			if tc.input.queued {
				candidate.JobID = "queued"
			}
			out, err := invokeReportExportComplete(context.Background(), rt, key, candidate)
			if (err != nil) != tc.expect.failure {
				t.Fatalf("complete output=%+v error=%v expected failure=%v", out, err, tc.expect.failure)
			}
			if err == nil && (out.Job == nil || out.Job.Status != "succeeded") {
				t.Fatalf("completion output=%+v", out)
			}
			jobID := "running"
			if tc.input.queued {
				jobID = "queued"
			}
			var status string
			must(t, db.QueryRow("SELECT status FROM report_export_job WHERE job_id=?", jobID).Scan(&status))
			if status != tc.expect.status {
				t.Fatalf("job status=%q expected=%q", status, tc.expect.status)
			}
			var count int
			must(t, db.QueryRow("SELECT COUNT(*) FROM report_export_artifact WHERE job_id=?", jobID).Scan(&count))
			if count != tc.expect.artifacts {
				t.Fatalf("artifact count=%d expected=%d", count, tc.expect.artifacts)
			}
		})
	}
}

func TestReportExportCompleteCallerTransaction(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type useCase struct {
		desc     string
		commit   bool
		expected string
	}
	for _, tc := range []useCase{{"caller rollback", false, "running"}, {"caller commit", true, "succeeded"}} {
		t.Run(tc.desc, func(t *testing.T) {
			db, _ := reportArtifactFixture(t, project)
			_, err := db.Exec("DELETE FROM report_export_artifact WHERE job_id='running'")
			must(t, err)
			db.SetMaxOpenConns(1)
			tx, err := db.BeginTx(context.Background(), nil)
			must(t, err)
			defer tx.Rollback()
			rt, key := reportExportCompleteRuntime(t, db, "u1", tx)
			out, err := invokeReportExportComplete(context.Background(), rt, key, reportExportCompleteInput())
			must(t, err)
			if out == nil || out.Job == nil || out.Job.Status != "succeeded" {
				t.Fatalf("pending completion=%+v", out)
			}
			var pending string
			must(t, tx.QueryRow("SELECT status FROM report_export_job WHERE job_id='running'").Scan(&pending))
			if pending != "succeeded" {
				t.Fatalf("pending status=%s", pending)
			}
			if tc.commit {
				must(t, tx.Commit())
			} else {
				must(t, tx.Rollback())
			}
			var stored string
			must(t, db.QueryRow("SELECT status FROM report_export_job WHERE job_id='running'").Scan(&stored))
			if stored != tc.expected {
				t.Fatalf("stored status=%s expected=%s", stored, tc.expected)
			}
			var count int
			must(t, db.QueryRow("SELECT COUNT(*) FROM report_export_artifact WHERE job_id='running'").Scan(&count))
			want := 0
			if tc.commit {
				want = 1
			}
			if count != want {
				t.Fatalf("stored artifact count=%d expected=%d", count, want)
			}
		})
	}
}

func TestReportExportCompleteHostRoutePrivate(t *testing.T) {
	holder := reflect.TypeFor[complete.Component]()
	field, ok := holder.FieldByName("Contract")
	if !ok {
		t.Fatal("completion contract is missing")
	}
	tag, present, err := dtag.ParseComponent(field.Tag)
	must(t, err)
	if !present {
		t.Fatal("completion component tag is missing")
	}
	source := &bootstrap.RouteSource{HolderType: holder.Name(), FieldName: field.Name, PackageName: "exportcomplete", PackagePath: holder.PkgPath(), Tag: tag, InputType: "Input", OutputType: "Output"}
	component, err := source.Resolve(reflect.TypeFor[complete.Input](), reflect.TypeFor[complete.Output]())
	must(t, err)
	if len(component.Routes) != 1 || !component.Routes[0].Internal {
		t.Fatalf("completion route is public: %+v", component.Routes)
	}
}
