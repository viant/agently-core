package tests

import (
	"context"
	"database/sql"
	"errors"
	"github.com/viant/datly/runtime/handler/provider"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"runtime"
	"sync"
	"testing"
	"time"

	read "github.com/viant/agently-core/internal/datly/reporting/context/read"
	write "github.com/viant/agently-core/internal/datly/reporting/context/write"
	contextstore "github.com/viant/agently-core/internal/store/reporting/context"
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

func reportContextFixture(t *testing.T, project string) (*sql.DB, string) {
	t.Helper()
	db, path := goalFixture(t, project)
	_, err := db.Exec(`INSERT INTO report_run(report_run_id,owner_id,materializer,status,started_at,revision,ui_run_request_id,created_at,updated_at) VALUES
 ('r1','u1','test','completed','2026-01-01 00:00:00',1,'req1','2026-01-01 00:00:00','2026-01-01 00:00:00'),
 ('r2','u1','test','completed','2026-01-01 00:00:00',1,'req2','2026-01-01 00:00:00','2026-01-01 00:00:00'),
 ('r3','u2','test','completed','2026-01-01 00:00:00',1,'req3','2026-01-01 00:00:00','2026-01-01 00:00:00');
 INSERT INTO conversation_report_context(owner_id,conversation_id,active_report_run_id,revision,activation_source,actor_id,updated_at) VALUES
 ('u1','c1','r1',1,'manual','actor1','2026-01-02 00:00:00'),
 ('u2','c3','r3',1,'manual','actor2','2026-01-02 00:00:00');`)
	must(t, err)
	return db, path
}

func reportContextRuntime(t *testing.T, db *sql.DB, subject string, internal bool, supplied *sql.Tx) (*druntime.Runtime, spec.Key, spec.Key) {
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

func reportContextMutation(owner, conversation, run string, expected, desired int64) (*write.Input, *write.Context) {
	entity := &write.Context{}
	entity.SetOwnerId(owner)
	entity.SetConversationId(conversation)
	entity.SetActiveReportRunId(run)
	entity.SetRevision(expected)
	entity.SetActivationSource("manual")
	entity.SetActorId("actor1")
	entity.SetUpdatedAt(time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC))
	input := &write.Input{}
	input.SetDesiredRevision(desired)
	input.SetContexts([]*write.Context{entity})
	return input, entity
}

func invokeReportContextWriter(ctx context.Context, rt *druntime.Runtime, key spec.Key, input *write.Input) error {
	_, err := rt.InvokeComponent(ctx, dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "PATCH", Path: "/v1/internal/forge/reporting/conversation-context"}}, Input: input})
	return err
}

func TestReportContextWriterCAS(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type input struct {
		owner, subject, conversation, run string
		expected, desired                 int64
	}
	type expect struct {
		failure  bool
		class    string
		revision int64
		run      string
	}
	type useCase struct {
		desc   string
		input  input
		expect expect
	}
	for _, tc := range []useCase{
		{"create from expected zero", input{"u1", "u1", "c2", "r2", 0, 1}, expect{revision: 1, run: "r2"}},
		{"update exact revision", input{"u1", "u1", "c1", "r2", 1, 2}, expect{revision: 2, run: "r2"}},
		{"stale revision rejected", input{"u1", "u1", "c1", "r2", 9, 10}, expect{failure: true, class: "cas", revision: 1, run: "r1"}},
		{"create cannot replace existing", input{"u1", "u1", "c1", "r2", 0, 1}, expect{failure: true, class: "cas", revision: 1, run: "r1"}},
		{"update missing context rejected", input{"u1", "u1", "c2", "r2", 1, 2}, expect{failure: true, class: "notfound"}},
		{"other owner cannot patch", input{"u1", "u2", "c1", "r2", 1, 2}, expect{failure: true, class: "notfound", revision: 1, run: "r1"}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			db, _ := reportContextFixture(t, project)
			adapterDB, _ := reportContextFixture(t, project)
			rt, _, key := reportContextRuntime(t, db, tc.input.subject, false, nil)
			adapterRT, _, _ := reportContextRuntime(t, adapterDB, tc.input.subject, false, nil)
			store := &contextstore.Store{Invoker: adapterRT, OwnerID: func(context.Context) string { return tc.input.subject }}
			mutation, _ := reportContextMutation(tc.input.owner, tc.input.conversation, tc.input.run, tc.input.expected, tc.input.desired)
			err := invokeReportContextWriter(context.Background(), rt, key, mutation)
			if (err != nil) != tc.expect.failure {
				t.Fatalf("CAS error=%v expected failure=%v", err, tc.expect.failure)
			}
			adapterErr := store.PutCAS(context.Background(), &contextstore.Record{
				OwnerID: tc.input.owner, ConversationID: tc.input.conversation, ActiveReportRunID: tc.input.run,
				Revision: tc.input.desired, ActivationSource: "manual", ActorID: "actor1",
				UpdatedAt: time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC),
			}, tc.input.expected)
			if (adapterErr != nil) != tc.expect.failure {
				t.Fatalf("adapter CAS error=%v expected failure=%v", adapterErr, tc.expect.failure)
			}
			switch tc.expect.class {
			case "cas":
				if !errors.Is(adapterErr, contextstore.ErrCASMismatch) {
					t.Fatalf("adapter CAS class=%v", adapterErr)
				}
			case "notfound":
				if !errors.Is(adapterErr, contextstore.ErrNotFound) {
					t.Fatalf("adapter notfound class=%v", adapterErr)
				}
			}
			var revision int64
			var runID string
			err = db.QueryRow("SELECT revision,active_report_run_id FROM conversation_report_context WHERE owner_id=? AND conversation_id=?", tc.input.owner, tc.input.conversation).Scan(&revision, &runID)
			var adapterRevision int64
			var adapterRunID string
			adapterRowErr := adapterDB.QueryRow("SELECT revision,active_report_run_id FROM conversation_report_context WHERE owner_id=? AND conversation_id=?", tc.input.owner, tc.input.conversation).Scan(&adapterRevision, &adapterRunID)
			if (err == nil) != (adapterRowErr == nil) || err == nil && (revision != adapterRevision || runID != adapterRunID) {
				t.Fatalf("adapter revision/run=(%d,%q,%v), native=(%d,%q,%v)", adapterRevision, adapterRunID, adapterRowErr, revision, runID, err)
			}
			if tc.expect.run == "" {
				if err != sql.ErrNoRows {
					t.Fatalf("expected no context, got revision=%d run=%q err=%v", revision, runID, err)
				}
				return
			}
			must(t, err)
			if revision != tc.expect.revision || runID != tc.expect.run {
				t.Fatalf("stored revision/run=(%d,%q), expected=(%d,%q)", revision, runID, tc.expect.revision, tc.expect.run)
			}
		})
	}
}

func TestReportContextReaderOwnerScope(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type input struct {
		subject, conversation string
		internal              bool
	}
	type useCase struct {
		desc   string
		input  input
		expect []string
	}
	for _, tc := range []useCase{
		{"owner sees own context", input{"u1", "c1", false}, []string{"u1:c1"}},
		{"other owner cannot see context", input{"u2", "c1", false}, []string{}},
		{"owner list stays scoped", input{"u2", "", false}, []string{"u2:c3"}},
		{"trusted internal sees all", input{"", "", true}, []string{"u1:c1", "u2:c3"}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			db, _ := reportContextFixture(t, project)
			rt, key, _ := reportContextRuntime(t, db, tc.input.subject, tc.input.internal, nil)
			query := &read.Input{}
			if tc.input.conversation != "" {
				query.SetConversationID(tc.input.conversation)
			}
			value, err := rt.InvokeComponent(context.Background(), dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "GET", Path: "/v1/internal/forge/reporting/conversation-context"}}, Input: query})
			must(t, err)
			keys := []string{}
			for _, row := range value.(*read.Output).Data {
				keys = append(keys, row.OwnerId+":"+row.ConversationId)
				if row.Revision != 1 || row.UpdatedAt.IsZero() {
					t.Fatalf("reader lost revision or timestamp: %+v", row)
				}
			}
			if !reflect.DeepEqual(keys, tc.expect) {
				t.Fatalf("keys=%v expected=%v", keys, tc.expect)
			}
		})
	}
	t.Run("HTTP query cannot elevate owner", func(t *testing.T) {
		db, _ := reportContextFixture(t, project)
		rt, _, _ := reportContextRuntime(t, db, "u2", false, nil)
		request := httptest.NewRequest("GET", "/v1/internal/forge/reporting/conversation-context?conversationId=c1&internal=true&ownerSubject=u1", nil)
		scope, err := requestprovider.New(request)
		must(t, err)
		defer scope.Close()
		value, err := rt.ExecuteRoute(context.Background(), "GET", "/v1/internal/forge/reporting/conversation-context", scope)
		must(t, err)
		if len(value.(*read.Output).Data) != 0 {
			t.Fatal("HTTP query replaced host owner scope")
		}
	})
}

func TestReportContextDeleteRevisionGuard(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type input struct {
		subject  string
		revision int64
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
		{"matching revision deletes", input{subject: "u1", revision: 1}, expect{remaining: 0}},
		{"stale revision rejects delete", input{subject: "u1", revision: 9}, expect{failure: true, remaining: 1}},
		{"foreign owner rejects delete", input{subject: "u2", revision: 1}, expect{failure: true, remaining: 1}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			db, _ := reportContextFixture(t, project)
			rt, _, key := reportContextRuntime(t, db, tc.input.subject, false, nil)
			entity := &write.Context{}
			entity.SetOwnerId("u1")
			entity.SetConversationId("c1")
			entity.SetRevision(tc.input.revision)
			entity.SetShouldDelete(true)
			input := &write.Input{}
			input.SetContexts([]*write.Context{entity})
			err := invokeReportContextWriter(context.Background(), rt, key, input)
			if (err != nil) != tc.expect.failure {
				t.Fatalf("delete error=%v expected failure=%v", err, tc.expect.failure)
			}
			var remaining int
			must(t, db.QueryRow("SELECT COUNT(*) FROM conversation_report_context WHERE owner_id='u1' AND conversation_id='c1'").Scan(&remaining))
			if remaining != tc.expect.remaining {
				t.Fatalf("remaining=%d expected=%d", remaining, tc.expect.remaining)
			}
		})
	}
}

func TestReportContextCallerTransaction(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type useCase struct {
		desc   string
		commit bool
		expect int64
	}
	for _, tc := range []useCase{{"caller rollback", false, 1}, {"caller commit", true, 2}} {
		t.Run(tc.desc, func(t *testing.T) {
			db, _ := reportContextFixture(t, project)
			db.SetMaxOpenConns(1)
			tx, err := db.BeginTx(context.Background(), nil)
			must(t, err)
			defer tx.Rollback()
			rt, _, key := reportContextRuntime(t, db, "u1", false, tx)
			mutation, _ := reportContextMutation("u1", "c1", "r2", 1, 2)
			must(t, invokeReportContextWriter(context.Background(), rt, key, mutation))
			var pending int64
			must(t, tx.QueryRow("SELECT revision FROM conversation_report_context WHERE owner_id='u1' AND conversation_id='c1'").Scan(&pending))
			if pending != 2 {
				t.Fatalf("pending revision=%d", pending)
			}
			if tc.commit {
				must(t, tx.Commit())
			} else {
				must(t, tx.Rollback())
			}
			var stored int64
			must(t, db.QueryRow("SELECT revision FROM conversation_report_context WHERE owner_id='u1' AND conversation_id='c1'").Scan(&stored))
			if stored != tc.expect {
				t.Fatalf("stored revision=%d expected=%d", stored, tc.expect)
			}
		})
	}
}

func TestReportContextIndependentConnectionsCAS(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	db, path := reportContextFixture(t, project)
	_, err := db.Exec("PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000")
	must(t, err)
	other, err := sql.Open("sqlite3", path+"?_foreign_keys=on&_busy_timeout=5000")
	must(t, err)
	defer other.Close()
	other.SetMaxOpenConns(1)
	_, err = other.Exec("PRAGMA busy_timeout=5000")
	must(t, err)
	first, _, firstKey := reportContextRuntime(t, db, "u1", false, nil)
	second, _, secondKey := reportContextRuntime(t, other, "u1", false, nil)
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
			mutation, _ := reportContextMutation("u1", "c1", "r2", 1, 2)
			results[index] = invokeReportContextWriter(context.Background(), runtimes[index], keys[index], mutation)
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
		t.Fatalf("CAS successes=%d, errors=%v", success, results)
	}
	var revision int64
	must(t, db.QueryRow("SELECT revision FROM conversation_report_context WHERE owner_id='u1' AND conversation_id='c1'").Scan(&revision))
	if revision != 2 {
		t.Fatalf("final revision=%d", revision)
	}
}

func TestReportContextStoreGet(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type input struct {
		subject, conversation string
	}
	type expect struct {
		found bool
		run   string
	}
	type useCase struct {
		desc   string
		input  input
		expect expect
	}
	for _, tc := range []useCase{
		{"owner gets current pointer", input{"u1", "c1"}, expect{found: true, run: "r1"}},
		{"foreign owner is not found", input{"u2", "c1"}, expect{}},
		{"missing context is not found", input{"u1", "c2"}, expect{}},
		{"anonymous lookup is not found", input{"", "c1"}, expect{}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			db, _ := reportContextFixture(t, project)
			rt, _, _ := reportContextRuntime(t, db, tc.input.subject, false, nil)
			store := &contextstore.Store{Invoker: rt, OwnerID: func(context.Context) string { return tc.input.subject }}
			row, err := store.Get(context.Background(), tc.input.conversation)
			if tc.expect.found && (err != nil || row == nil) {
				t.Fatalf("native get=(%+v,%v)", row, err)
			}
			if !tc.expect.found && !errors.Is(err, contextstore.ErrNotFound) {
				t.Fatalf("native get error=%v, want not found", err)
			}
			if tc.expect.found {
				if row.OwnerID != tc.input.subject || row.ConversationID != tc.input.conversation || row.ActiveReportRunID != tc.expect.run || row.Revision != 1 {
					t.Fatalf("native=%+v expected run=%q revision=1", row, tc.expect.run)
				}
			}
		})
	}
}
