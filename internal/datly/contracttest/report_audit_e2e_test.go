package tests

import (
	"context"
	"database/sql"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/google/uuid"
	read "github.com/viant/agently-core/internal/datly/reporting/audit/read"
	write "github.com/viant/agently-core/internal/datly/reporting/audit/write"
	reportaudit "github.com/viant/agently-core/internal/store/reportaudit"
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

func reportAuditFixture(t *testing.T, project string) *sql.DB {
	t.Helper()
	db, _ := goalFixture(t, project)
	_, err := db.Exec(`INSERT INTO report_audit_event(event_id,event_type,artifact_ref,version,job_id,artifact_id,actor_id,occurred_at) VALUES
 ('a','created','ref',1,'j1','x1','u1','2026-01-01 01:00:00'),
 ('b','exported','ref',1,'j1','x2','u1','2026-01-01 02:00:00'),
 ('c','downloaded','ref',2,'j2','x2','u2','2026-01-01 03:00:00')`)
	must(t, err)
	return db
}

func reportAuditRuntime(t *testing.T, db *sql.DB, trusted bool, supplied *sql.Tx) (*druntime.Runtime, spec.Key, spec.Key) {
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
	access := ordinaryAccess("reportauditaccess", func(context.Context, reflect.Type, string) (any, bool, error) { return trusted, true, nil })
	rt, err := druntime.NewRuntime([]*registry.RegisteredComponent{
		{Component: r.Component, Input: r.Input, Output: r.Output, OutputType: reflect.TypeFor[read.Output](), Reader: reader, Providers: []locator.Provider{access}},
		{Component: w.Component, Input: w.Input, Output: w.Output, OutputType: reflect.TypeFor[write.Output](), Handler: handler, Providers: []locator.Provider{access, views}, DataSource: dml.Source{DB: db, Tx: supplied}},
	}, druntime.WithResources(resources))
	must(t, err)
	return rt, r.Component.Key, w.Component.Key
}

func TestReportAuditApplicationImportIfAbsent(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	db := reportAuditFixture(t, filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file)))))
	rt, _, _ := reportAuditRuntime(t, db, true, nil)
	store := &reportaudit.Store{Invoker: rt}
	event := reportaudit.Event{
		ID: "imported-event", Type: "report.saved", ArtifactRef: "report://saved",
		Version: 3, JobID: "j1", ArtifactID: "x1", ActorID: "u1",
		ActorRef: "user://u1", OccurredAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		MetadataJSON: []byte(`{"source":"filesystem"}`),
	}
	for i := 0; i < 2; i++ {
		must(t, store.ImportIfAbsent(context.Background(), event))
	}
	var count int
	var kind, ref, actorRef, metadata string
	var version int64
	must(t, db.QueryRow(`SELECT COUNT(*), MAX(event_type), MAX(artifact_ref), MAX(version), MAX(actor_ref), MAX(metadata_json) FROM report_audit_event WHERE event_id=?`, event.ID).Scan(&count, &kind, &ref, &version, &actorRef, &metadata))
	if count != 1 || kind != event.Type || ref != event.ArtifactRef || version != event.Version || actorRef != event.ActorRef || metadata != string(event.MetadataJSON) {
		t.Fatalf("imported event count=%d type=%q ref=%q version=%d actorRef=%q metadata=%q", count, kind, ref, version, actorRef, metadata)
	}
}

func reportAuditRead(ctx context.Context, rt *druntime.Runtime, key spec.Key, input *read.Input) ([]*read.AuditEvent, error) {
	value, err := rt.InvokeComponent(ctx, dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "GET", Path: "/v1/internal/forge/reporting/audit"}}, Input: input})
	if err != nil {
		return nil, err
	}
	return value.(*read.Output).Data, nil
}

func reportAuditWrite(ctx context.Context, rt *druntime.Runtime, key spec.Key, input *write.Input) error {
	_, err := rt.InvokeComponent(ctx, dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "PATCH", Path: "/v1/internal/forge/reporting/audit"}}, Input: input})
	return err
}

func reportAuditIDs(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query("SELECT event_id FROM report_audit_event ORDER BY event_id")
	must(t, err)
	defer rows.Close()
	result := []string{}
	for rows.Next() {
		var id string
		must(t, rows.Scan(&id))
		result = append(result, id)
	}
	must(t, rows.Err())
	return result
}

func TestReportAuditLegacyDeletionParity(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type input struct {
		kind, id string
		reject   bool
	}
	type expect struct {
		failure   bool
		remaining []string
	}
	type useCase struct {
		desc   string
		input  input
		expect expect
	}
	for _, tc := range []useCase{
		{"delete by job", input{kind: "job", id: "j1"}, expect{remaining: []string{"c"}}},
		{"delete by artifact", input{kind: "artifact", id: "x2"}, expect{remaining: []string{"a"}}},
		{"missing job no-op", input{kind: "job", id: "missing"}, expect{remaining: []string{"a", "b", "c"}}},
		{"late job deletion rollback", input{kind: "job", id: "j1", reject: true}, expect{failure: true, remaining: []string{"a", "b", "c"}}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			oldDB, db := reportAuditFixture(t, project), reportAuditFixture(t, project)
			if tc.input.reject {
				for _, fixture := range []*sql.DB{oldDB, db} {
					_, err := fixture.Exec("CREATE TRIGGER reject_audit_b BEFORE DELETE ON report_audit_event WHEN OLD.event_id='b' BEGIN SELECT RAISE(ABORT,'fixture late audit rejection'); END")
					must(t, err)
				}
			}
			column := "job_id"
			if tc.input.kind == "artifact" {
				column = "artifact_id"
			}
			_, oldErr := oldDB.Exec("DELETE FROM report_audit_event WHERE "+column+"=?", tc.input.id)
			rt, rkey, wkey := reportAuditRuntime(t, db, true, nil)
			query := &read.Input{}
			if tc.input.kind == "job" {
				query.SetJobID(tc.input.id)
			} else {
				query.SetArtifactID(tc.input.id)
			}
			rows, err := reportAuditRead(context.Background(), rt, rkey, query)
			must(t, err)
			deletions := make([]*write.AuditEvent, 0, len(rows))
			for _, row := range rows {
				item := &write.AuditEvent{}
				item.SetEventId(row.EventId)
				item.SetShouldDelete(true)
				deletions = append(deletions, item)
			}
			mutation := &write.Input{}
			if tc.input.kind == "job" {
				mutation.SetExpectedJobID(tc.input.id)
			} else {
				mutation.SetExpectedArtifactID(tc.input.id)
			}
			mutation.SetEvents(deletions)
			err = reportAuditWrite(context.Background(), rt, wkey, mutation)
			if (oldErr != nil) != tc.expect.failure || (err != nil) != tc.expect.failure {
				t.Fatalf("legacy=%v native=%v expected failure=%v", oldErr, err, tc.expect.failure)
			}
			if oldRows, newRows := reportAuditIDs(t, oldDB), reportAuditIDs(t, db); !reflect.DeepEqual(oldRows, newRows) || !reflect.DeepEqual(newRows, tc.expect.remaining) {
				t.Fatalf("legacy=%v native=%v expected=%v", oldRows, newRows, tc.expect.remaining)
			}
		})
	}
}

func TestReportAuditGuardsAndAppendOnly(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type input struct {
		id, job, artifact string
		trusted, insert   bool
	}
	type expect struct {
		failure   bool
		remaining []string
	}
	type useCase struct {
		desc   string
		input  input
		expect expect
	}
	for _, tc := range []useCase{
		{"matching job deletes", input{id: "a", job: "j1", trusted: true}, expect{remaining: []string{"b", "c"}}},
		{"wrong job blocked", input{id: "a", job: "j2", trusted: true}, expect{failure: true, remaining: []string{"a", "b", "c"}}},
		{"matching artifact deletes", input{id: "a", artifact: "x1", trusted: true}, expect{remaining: []string{"b", "c"}}},
		{"wrong artifact blocked", input{id: "a", artifact: "x2", trusted: true}, expect{failure: true, remaining: []string{"a", "b", "c"}}},
		{"missing guard rejected", input{id: "a", trusted: true}, expect{failure: true, remaining: []string{"a", "b", "c"}}},
		{"untrusted delete rejected", input{id: "a", job: "j1"}, expect{failure: true, remaining: []string{"a", "b", "c"}}},
		{"append event with timestamp default", input{id: "new", trusted: true, insert: true}, expect{remaining: []string{"a", "b", "c", "new"}}},
		{"duplicate event cannot update", input{id: "a", trusted: true, insert: true}, expect{failure: true, remaining: []string{"a", "b", "c"}}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			db := reportAuditFixture(t, project)
			rt, _, key := reportAuditRuntime(t, db, tc.input.trusted, nil)
			item := &write.AuditEvent{}
			item.SetEventId(tc.input.id)
			if tc.input.insert {
				item.SetEventType("created")
				item.SetArtifactRef("ref")
				item.SetActorId("u1")
			} else {
				item.SetShouldDelete(true)
			}
			mutation := &write.Input{}
			if tc.input.job != "" {
				mutation.SetExpectedJobID(tc.input.job)
			}
			if tc.input.artifact != "" {
				mutation.SetExpectedArtifactID(tc.input.artifact)
			}
			mutation.SetEvents([]*write.AuditEvent{item})
			err := reportAuditWrite(context.Background(), rt, key, mutation)
			if (err != nil) != tc.expect.failure {
				t.Fatalf("mutation error=%v expected failure=%v", err, tc.expect.failure)
			}
			if rows := reportAuditIDs(t, db); !reflect.DeepEqual(rows, tc.expect.remaining) {
				t.Fatalf("rows=%v expected=%v", rows, tc.expect.remaining)
			}
			if tc.input.id == "new" {
				var at time.Time
				must(t, db.QueryRow("SELECT occurred_at FROM report_audit_event WHERE event_id='new'").Scan(&at))
				if at.IsZero() {
					t.Fatal("missing timestamp default")
				}
			}
		})
	}
}

func TestReportAuditReaderScope(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type input struct {
		job, artifact, actor string
		trusted              bool
	}
	type useCase struct {
		desc   string
		input  input
		expect []string
	}
	for _, tc := range []useCase{
		{"job predicate", input{job: "j1", trusted: true}, []string{"a", "b"}},
		{"artifact predicate", input{artifact: "x2", trusted: true}, []string{"b", "c"}},
		{"actor predicate", input{actor: "u2", trusted: true}, []string{"c"}},
		{"predicate intersection", input{job: "j1", actor: "u2", trusted: true}, []string{}},
		{"untrusted has no rows", input{job: "j1"}, []string{}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			db := reportAuditFixture(t, project)
			rt, key, _ := reportAuditRuntime(t, db, tc.input.trusted, nil)
			query := &read.Input{}
			if tc.input.job != "" {
				query.SetJobID(tc.input.job)
			}
			if tc.input.artifact != "" {
				query.SetArtifactID(tc.input.artifact)
			}
			if tc.input.actor != "" {
				query.SetActorID(tc.input.actor)
			}
			rows, err := reportAuditRead(context.Background(), rt, key, query)
			must(t, err)
			ids := []string{}
			for _, row := range rows {
				ids = append(ids, row.EventId)
			}
			if !reflect.DeepEqual(ids, tc.expect) {
				t.Fatalf("ids=%v expected=%v", ids, tc.expect)
			}
		})
	}
	t.Run("HTTP query cannot replace trusted access", func(t *testing.T) {
		db := reportAuditFixture(t, project)
		rt, _, _ := reportAuditRuntime(t, db, false, nil)
		request := httptest.NewRequest("GET", "/v1/internal/forge/reporting/audit?jobId=j1&trusted=true", nil)
		scope, err := requestprovider.New(request)
		must(t, err)
		defer scope.Close()
		value, err := rt.ExecuteRoute(context.Background(), "GET", "/v1/internal/forge/reporting/audit", scope)
		must(t, err)
		if len(value.(*read.Output).Data) != 0 {
			t.Fatal("HTTP query replaced host trust")
		}
	})
}

func TestReportAuditCallerTransaction(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type useCase struct {
		desc   string
		commit bool
		expect int
	}
	for _, tc := range []useCase{{"caller rollback", false, 1}, {"caller commit", true, 0}} {
		t.Run(tc.desc, func(t *testing.T) {
			db := reportAuditFixture(t, project)
			db.SetMaxOpenConns(1)
			tx, err := db.BeginTx(context.Background(), nil)
			must(t, err)
			defer tx.Rollback()
			rt, _, key := reportAuditRuntime(t, db, true, tx)
			item := &write.AuditEvent{}
			item.SetEventId("a")
			item.SetShouldDelete(true)
			input := &write.Input{}
			input.SetExpectedJobID("j1")
			input.SetEvents([]*write.AuditEvent{item})
			must(t, reportAuditWrite(context.Background(), rt, key, input))
			var pending int
			must(t, tx.QueryRow("SELECT COUNT(*) FROM report_audit_event WHERE event_id='a'").Scan(&pending))
			if pending != 0 {
				t.Fatalf("pending count=%d", pending)
			}
			if tc.commit {
				must(t, tx.Commit())
			} else {
				must(t, tx.Rollback())
			}
			var stored int
			must(t, db.QueryRow("SELECT COUNT(*) FROM report_audit_event WHERE event_id='a'").Scan(&stored))
			if stored != tc.expect {
				t.Fatalf("stored count=%d expected=%d", stored, tc.expect)
			}
		})
	}
}

func TestReportAuditGeneratedEventID(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type input struct{ id string }
	type expect struct{ eventType, artifactRef, actorID string }
	type useCase struct {
		desc   string
		input  input
		expect expect
	}
	for _, tc := range []useCase{
		{"missing id allocates uuid", input{}, expect{"viewed", "ref-new", "u-new"}},
		{"blank id allocates uuid", input{id: "  "}, expect{"viewed", "ref-new", "u-new"}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			db := reportAuditFixture(t, project)
			rt, _, key := reportAuditRuntime(t, db, true, nil)
			item := &write.AuditEvent{}
			item.SetEventId(tc.input.id)
			item.SetEventType(tc.expect.eventType)
			item.SetArtifactRef(tc.expect.artifactRef)
			item.SetActorId(tc.expect.actorID)
			item.SetMetadataJson([]byte(`{"source":"ui"}`))
			in := &write.Input{}
			in.SetEvents([]*write.AuditEvent{item})
			must(t, reportAuditWrite(context.Background(), rt, key, in))
			var id, eventType, artifactRef, actorID string
			var metadata []byte
			must(t, db.QueryRow("SELECT event_id,event_type,artifact_ref,actor_id,metadata_json FROM report_audit_event WHERE actor_id='u-new'").Scan(&id, &eventType, &artifactRef, &actorID, &metadata))
			if _, err := uuid.Parse(id); err != nil {
				t.Fatalf("generated event id %q: %v", id, err)
			}
			if eventType != tc.expect.eventType || artifactRef != tc.expect.artifactRef || actorID != tc.expect.actorID || string(metadata) != `{"source":"ui"}` {
				t.Fatalf("stored fields=(%s,%s,%s,%s)", eventType, artifactRef, actorID, metadata)
			}
		})
	}
}
