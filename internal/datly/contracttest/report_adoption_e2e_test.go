package tests

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/viant/datly/runtime/handler/provider"
	"path/filepath"
	"reflect"
	"runtime"
	"sync"
	"testing"
	"time"

	contextread "github.com/viant/agently-core/internal/datly/reporting/context/read"
	contextwrite "github.com/viant/agently-core/internal/datly/reporting/context/write"
	runread "github.com/viant/agently-core/internal/datly/reporting/run/read"
	runwrite "github.com/viant/agently-core/internal/datly/reporting/run/write"
	adoption "github.com/viant/agently-core/internal/store/reporting/adoption"
	contextstore "github.com/viant/agently-core/internal/store/reporting/context"
	runstore "github.com/viant/agently-core/internal/store/reporting/run"
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

func TestReportAdoptionHostRoutePrivate(t *testing.T) {
	holder := reflect.TypeFor[adoption.Component]()
	field, ok := holder.FieldByName("Contract")
	if !ok {
		t.Fatal("adoption contract is missing")
	}
	tag, present, err := dtag.ParseComponent(field.Tag)
	must(t, err)
	if !present {
		t.Fatal("adoption component tag is missing")
	}
	source := &bootstrap.RouteSource{HolderType: holder.Name(), FieldName: field.Name, PackageName: "adoption", PackagePath: holder.PkgPath(), Tag: tag, InputType: "Input", OutputType: "Output"}
	component, err := source.Resolve(reflect.TypeFor[adoption.Input](), reflect.TypeFor[adoption.Output]())
	must(t, err)
	if len(component.Routes) != 1 || !component.Routes[0].Internal {
		t.Fatalf("adoption route is public: %+v", component.Routes)
	}
}

func TestReportAdoptionIndependentConnections(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	db, path := reportRunFixture(t, project)
	_, err := db.Exec("PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000")
	must(t, err)
	other, err := sql.Open("sqlite3", path+"?_foreign_keys=on&_busy_timeout=5000")
	must(t, err)
	defer other.Close()
	other.SetMaxOpenConns(1)
	_, err = other.Exec("PRAGMA busy_timeout=5000")
	must(t, err)
	first, firstKey := reportAdoptionRuntime(t, db, "u1", nil)
	second, secondKey := reportAdoptionRuntime(t, other, "u1", nil)
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
			_, results[index] = runtimes[index].InvokeComponent(context.Background(), dexec.ComponentRequest{
				Target: dexec.ComponentTarget{Component: keys[index], Route: spec.RouteRef{Method: "POST", Path: "/v1/internal/forge/reporting/adopt"}},
				Input:  reportAdoptionInput(2, 0),
			})
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
		t.Fatalf("adoption successes=%d errors=%v", success, results)
	}
	runRevision, conversation, contextRevision := reportAdoptionState(t, db)
	if runRevision != 3 || conversation != "c1" || contextRevision != 1 {
		t.Fatalf("concurrent adoption state=(%d,%q,%d)", runRevision, conversation, contextRevision)
	}
}

func reportAdoptionRuntime(t *testing.T, db *sql.DB, subject string, supplied *sql.Tx) (*druntime.Runtime, spec.Key) {
	t.Helper()
	resources := resource.New()
	must(t, resources.Register(runread.ReaderDatlyResourceNamespace, runread.ReaderDatlyResources))
	must(t, resources.Register(runwrite.WriterDatlyResourceNamespace, runwrite.WriterDatlyResources))
	must(t, resources.Register(contextread.ReaderDatlyResourceNamespace, contextread.ReaderDatlyResources))
	must(t, resources.Register(contextwrite.WriterDatlyResourceNamespace, contextwrite.WriterDatlyResources))
	rr := payloadArtifact(t, resources, reflect.TypeFor[runread.ReaderComponent](), reflect.TypeFor[runread.Input](), reflect.TypeFor[runread.Output]())
	rw := payloadArtifact(t, resources, reflect.TypeFor[runwrite.WriterComponent](), reflect.TypeFor[runwrite.Input](), reflect.TypeFor[runwrite.Output]())
	cr := payloadArtifact(t, resources, reflect.TypeFor[contextread.ReaderComponent](), reflect.TypeFor[contextread.Input](), reflect.TypeFor[contextread.Output]())
	cw := payloadArtifact(t, resources, reflect.TypeFor[contextwrite.WriterComponent](), reflect.TypeFor[contextwrite.Input](), reflect.TypeFor[contextwrite.Output]())
	parent := payloadArtifact(t, resources, reflect.TypeFor[adoption.Component](), reflect.TypeFor[adoption.Input](), reflect.TypeFor[adoption.Output]())
	handler, err := (adoption.Component{}).DatlyHandler("NewAdoption")()
	must(t, err)
	builder, err := bootstrap.NewArtifactBuilder(nil)
	must(t, err)
	parent, err = builder.Build(bootstrap.ArtifactInput{Component: parent.Component, InputType: reflect.TypeFor[adoption.Input](), OutputType: reflect.TypeFor[adoption.Output](), Resources: resources, Handler: handler})
	must(t, err)
	visibility := provider.Named("visibility", func(context.Context, reflect.Type, string) (any, bool, error) { return &subject, true, nil })
	access := ordinaryAccess("reportaccess", func(context.Context, reflect.Type, string) (any, bool, error) { return false, true, nil })
	parentRegistration, err := parent.Registration(registry.RegisteredComponent{DataSource: dml.Source{DB: db, Tx: supplied}, Providers: []locator.Provider{visibility, access}})
	must(t, err)
	runReader, err := rr.ReaderCompilation().NewExecution(bootstrap.ReaderRuntimeConfig{SQL: &dsql.SQLComponent{DB: db}})
	must(t, err)
	contextReader, err := cr.ReaderCompilation().NewExecution(bootstrap.ReaderRuntimeConfig{SQL: &dsql.SQLComponent{DB: db}})
	must(t, err)
	runViews, err := viewprovider.New(viewprovider.Config{Dependencies: rw.ViewDependencies, Input: rw.Input, SQL: &dsql.SQLComponent{DB: db}})
	must(t, err)
	contextViews, err := viewprovider.New(viewprovider.Config{Dependencies: cw.ViewDependencies, Input: cw.Input, SQL: &dsql.SQLComponent{DB: db}})
	must(t, err)
	runWriter, err := writer.New(rw.Component, reflect.TypeFor[runwrite.Input](), reflect.TypeFor[runwrite.Output](), "patch")
	must(t, err)
	contextWriter, err := writer.New(cw.Component, reflect.TypeFor[contextwrite.Input](), reflect.TypeFor[contextwrite.Output](), "patch")
	must(t, err)
	rt, err := druntime.NewRuntime([]*registry.RegisteredComponent{
		{Component: rr.Component, Input: rr.Input, Output: rr.Output, OutputType: reflect.TypeFor[runread.Output](), Reader: runReader, Providers: []locator.Provider{visibility, access}},
		{Component: cr.Component, Input: cr.Input, Output: cr.Output, OutputType: reflect.TypeFor[contextread.Output](), Reader: contextReader, Providers: []locator.Provider{visibility, access}},
		{Component: rw.Component, Input: rw.Input, Output: rw.Output, OutputType: reflect.TypeFor[runwrite.Output](), Handler: runWriter, Providers: []locator.Provider{visibility, access, runViews}, DataSource: dml.Source{DB: db}},
		{Component: cw.Component, Input: cw.Input, Output: cw.Output, OutputType: reflect.TypeFor[contextwrite.Output](), Handler: contextWriter, Providers: []locator.Provider{visibility, access, contextViews}, DataSource: dml.Source{DB: db}},
		parentRegistration,
	}, druntime.WithResources(resources))
	must(t, err)
	return rt, parent.Component.Key
}

func reportAdoptionInput(expectedRun, expectedContext int64) *adoption.Input {
	completed := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	return &adoption.Input{
		Run: &runstore.Record{
			ReportRunID: "manual", OwnerID: "u1", ConversationID: "c1", Materializer: "test", Origin: "manual",
			Status: "completed", StartedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), CompletedAt: &completed,
			Revision: expectedRun + 1, UIRunRequestID: "req-manual", ReportSpec: json.RawMessage(`{}`),
			AdoptionSource: "manual-adopt", ActorID: "u1", CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			UpdatedAt: time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC),
		},
		Context: &contextstore.Record{
			OwnerID: "u1", ConversationID: "c1", ActiveReportRunID: "manual", Revision: expectedContext + 1,
			ActivationSource: "manual", ActorID: "u1", UpdatedAt: time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC),
		},
		ExpectedRunRevision: expectedRun, ExpectedContextRevision: expectedContext,
	}
}

func reportAdoptionState(t *testing.T, db *sql.DB) (int64, string, int64) {
	t.Helper()
	var runRevision int64
	var conversation string
	must(t, db.QueryRow("SELECT revision,COALESCE(conversation_id,'') FROM report_run WHERE report_run_id='manual'").Scan(&runRevision, &conversation))
	var contextRevision int64
	err := db.QueryRow("SELECT revision FROM conversation_report_context WHERE owner_id='u1' AND conversation_id='c1'").Scan(&contextRevision)
	if err != nil && err != sql.ErrNoRows {
		t.Fatal(err)
	}
	return runRevision, conversation, contextRevision
}

func TestReportAdoptionSharedTransaction(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type input struct {
		existingPointer, staleRun, stalePointer, alteredSnapshot, foreignOwner, rejectPointer, callerTx, commit bool
	}
	type expect struct {
		failure                      bool
		runRevision, contextRevision int64
		conversation                 string
	}
	type useCase struct {
		desc   string
		input  input
		expect expect
	}
	for _, tc := range []useCase{
		{"new pointer adopts completed run atomically", input{}, expect{runRevision: 3, contextRevision: 1, conversation: "c1"}},
		{"existing pointer advances exact revision", input{existingPointer: true}, expect{runRevision: 3, contextRevision: 2, conversation: "c1"}},
		{"stale run revision fails before writes", input{staleRun: true}, expect{failure: true, runRevision: 2}},
		{"stale context revision fails before writes", input{stalePointer: true}, expect{failure: true, runRevision: 2}},
		{"completed snapshot remains immutable", input{alteredSnapshot: true}, expect{failure: true, runRevision: 2}},
		{"foreign owner cannot adopt", input{foreignOwner: true}, expect{failure: true, runRevision: 2}},
		{"late pointer failure rolls back run adoption", input{rejectPointer: true}, expect{failure: true, runRevision: 2}},
		{"caller transaction rollback retains ownership", input{callerTx: true}, expect{runRevision: 2}},
		{"caller transaction commit retains ownership", input{callerTx: true, commit: true}, expect{runRevision: 3, contextRevision: 1, conversation: "c1"}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			db, _ := reportRunFixture(t, project)
			if tc.input.existingPointer {
				for _, fixture := range []*sql.DB{db} {
					_, err := fixture.Exec("INSERT INTO conversation_report_context(owner_id,conversation_id,active_report_run_id,revision,activation_source,actor_id,updated_at) VALUES('u1','c1','running',1,'prior','u1','2026-01-02 00:00:00')")
					must(t, err)
				}
			}
			if tc.input.rejectPointer {
				for _, fixture := range []*sql.DB{db} {
					_, err := fixture.Exec("CREATE TRIGGER reject_report_pointer BEFORE INSERT ON conversation_report_context BEGIN SELECT RAISE(ABORT,'fixture pointer failure'); END")
					must(t, err)
				}
			}
			db.SetMaxOpenConns(1)
			var tx *sql.Tx
			var err error
			if tc.input.callerTx {
				tx, err = db.BeginTx(context.Background(), nil)
				must(t, err)
				defer tx.Rollback()
			}
			owner := "u1"
			if tc.input.foreignOwner {
				owner = "u2"
			}
			rt, key := reportAdoptionRuntime(t, db, owner, tx)
			expectedRun, expectedPointer := int64(2), int64(0)
			if tc.input.existingPointer {
				expectedPointer = 1
			}
			if tc.input.staleRun {
				expectedRun = 1
			}
			if tc.input.stalePointer {
				expectedPointer = 9
			}
			in := reportAdoptionInput(expectedRun, expectedPointer)
			if tc.input.alteredSnapshot {
				in.Run.ReportSpec = json.RawMessage(`{"changed":true}`)
			}
			value, err := rt.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "POST", Path: "/v1/internal/forge/reporting/adopt"}}, Input: in})
			if (err != nil) != tc.expect.failure {
				t.Fatalf("adoption result=%T error=%v expected failure=%v", value, err, tc.expect.failure)
			}
			if tc.input.staleRun || tc.input.stalePointer {
				if !errors.Is(err, adoption.ErrCASMismatch) {
					t.Fatalf("stale adoption error=%v", err)
				}
			}
			if tc.input.alteredSnapshot && !errors.Is(err, adoption.ErrImmutable) {
				t.Fatalf("snapshot mutation error=%v", err)
			}
			if tc.input.foreignOwner && !errors.Is(err, adoption.ErrNotFound) {
				t.Fatalf("foreign adoption error=%v", err)
			}
			if err == nil && (value.(*adoption.Output).RunRevision != 3 || value.(*adoption.Output).ContextRevision != in.Context.Revision) {
				t.Fatalf("adoption output=%+v", value)
			}
			if tx != nil {
				pendingRun, pendingConversation, pendingPointer := reportAdoptionStateTx(t, tx)
				if pendingRun != 3 || pendingConversation != "c1" || pendingPointer != 1 {
					t.Fatalf("caller pending state=(%d,%q,%d)", pendingRun, pendingConversation, pendingPointer)
				}
				if tc.input.commit {
					must(t, tx.Commit())
				} else {
					must(t, tx.Rollback())
				}
			}
			runRevision, conversation, contextRevision := reportAdoptionState(t, db)
			if runRevision != tc.expect.runRevision || conversation != tc.expect.conversation || contextRevision != tc.expect.contextRevision {
				t.Fatalf("stored state=(%d,%q,%d), expected=%+v", runRevision, conversation, contextRevision, tc.expect)
			}
		})
	}
}

func reportAdoptionStateTx(t *testing.T, tx *sql.Tx) (int64, string, int64) {
	t.Helper()
	var runRevision, contextRevision int64
	var conversation string
	must(t, tx.QueryRow("SELECT revision,COALESCE(conversation_id,'') FROM report_run WHERE report_run_id='manual'").Scan(&runRevision, &conversation))
	must(t, tx.QueryRow("SELECT revision FROM conversation_report_context WHERE owner_id='u1' AND conversation_id='c1'").Scan(&contextRevision))
	return runRevision, conversation, contextRevision
}
