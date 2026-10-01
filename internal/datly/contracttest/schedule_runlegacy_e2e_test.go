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

	read "github.com/viant/agently-core/internal/datly/legacyrun/read"
	write "github.com/viant/agently-core/internal/datly/legacyrun/write"
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

func legacyScheduleRunFixture(t *testing.T, project string) (*sql.DB, string) {
	t.Helper()
	db, path := goalFixture(t, project)
	_, err := db.Exec(`INSERT INTO schedule(id,name,agent_ref,created_by_user_id,created_at) VALUES
 ('s1','First','agent','u1','2026-01-01 00:00:00'),('s2','Second','agent','u1','2026-01-01 00:00:00');
 INSERT INTO schedule_run(id,schedule_id,conversation_id,status,conversation_kind,created_at,updated_at,lease_until) VALUES
 ('a','s1','c1','succeeded','scheduled','2026-01-01 01:00:00','2026-01-01 02:00:00',NULL),
 ('b','s1','c1','failed','scheduled','2026-01-01 03:00:00','2026-01-01 04:00:00',NULL),
 ('c','s2','c2','running','scheduled','2026-01-01 05:00:00','2026-01-01 06:00:00','2027-01-01 00:00:00')`)
	must(t, err)
	return db, path
}

func legacyScheduleRunRuntime(t *testing.T, db *sql.DB, trusted bool, supplied *sql.Tx) (*druntime.Runtime, spec.Key, spec.Key) {
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
	access := ordinaryAccess("schedulerunaccess", func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
		if name == "internal" {
			return trusted, true, nil
		}
		return nil, false, nil
	})
	rt, err := druntime.NewRuntime([]*registry.RegisteredComponent{
		{Component: r.Component, Input: r.Input, Output: r.Output, OutputType: reflect.TypeFor[read.Output](), Reader: reader, Providers: []locator.Provider{access}},
		{Component: w.Component, Input: w.Input, Output: w.Output, OutputType: reflect.TypeFor[write.Output](), Handler: handler, Providers: []locator.Provider{access, views}, DataSource: dml.Source{DB: db, Tx: supplied}},
	}, druntime.WithResources(resources))
	must(t, err)
	return rt, r.Component.Key, w.Component.Key
}

func legacyScheduleRows(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query("SELECT id FROM schedule_run ORDER BY id")
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

func invokeLegacyScheduleReader(ctx context.Context, rt *druntime.Runtime, key spec.Key, input *read.Input) ([]*read.LegacyRun, error) {
	value, err := rt.InvokeComponent(ctx, dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "GET", Path: "/v1/internal/agently/scheduler/legacy-run"}}, Input: input})
	if err != nil {
		return nil, err
	}
	return value.(*read.Output).Data, nil
}

func invokeLegacyScheduleWriter(ctx context.Context, rt *druntime.Runtime, key spec.Key, input *write.Input) error {
	_, err := rt.InvokeComponent(ctx, dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "PATCH", Path: "/v1/internal/agently/scheduler/legacy-run"}}, Input: input})
	return err
}

func TestLegacyScheduleRunDeletionParity(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type input struct {
		conversation string
		reject       bool
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
		{"delete by conversation", input{conversation: "c1"}, expect{remaining: []string{"c"}}},
		{"missing conversation no-op", input{conversation: "missing"}, expect{remaining: []string{"a", "b", "c"}}},
		{"late failure rolls back deletion batch", input{conversation: "c1", reject: true}, expect{failure: true, remaining: []string{"a", "b", "c"}}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			oldDB, _ := legacyScheduleRunFixture(t, project)
			db, _ := legacyScheduleRunFixture(t, project)
			if tc.input.reject {
				for _, fixture := range []*sql.DB{oldDB, db} {
					_, err := fixture.Exec("CREATE TRIGGER reject_schedule_run_b BEFORE DELETE ON schedule_run WHEN OLD.id='b' BEGIN SELECT RAISE(ABORT,'fixture late schedule-run rejection'); END")
					must(t, err)
				}
			}
			_, oldErr := oldDB.Exec("DELETE FROM schedule_run WHERE conversation_id=?", tc.input.conversation)
			rt, readerKey, writerKey := legacyScheduleRunRuntime(t, db, true, nil)
			query := &read.Input{}
			query.SetConversationID(tc.input.conversation)
			rows, err := invokeLegacyScheduleReader(context.Background(), rt, readerKey, query)
			must(t, err)
			deletions := make([]*write.LegacyRun, 0, len(rows))
			for _, row := range rows {
				item := &write.LegacyRun{}
				item.SetId(row.Id)
				item.SetShouldDelete(true)
				deletions = append(deletions, item)
			}
			mutation := &write.Input{}
			mutation.SetExpectedScheduleID("s1")
			mutation.SetRuns(deletions)
			err = invokeLegacyScheduleWriter(context.Background(), rt, writerKey, mutation)
			if (oldErr != nil) != tc.expect.failure || (err != nil) != tc.expect.failure {
				t.Fatalf("legacy=%v native=%v expected failure=%v", oldErr, err, tc.expect.failure)
			}
			oldRows, newRows := legacyScheduleRows(t, oldDB), legacyScheduleRows(t, db)
			if !reflect.DeepEqual(oldRows, newRows) || !reflect.DeepEqual(newRows, tc.expect.remaining) {
				t.Fatalf("legacy=%v native=%v expected=%v", oldRows, newRows, tc.expect.remaining)
			}
		})
	}
}

func TestLegacyScheduleRunGuardAndDefaults(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type input struct {
		id, expectedSchedule string
		trusted, insert      bool
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
		{"matching schedule deletes", input{"a", "s1", true, false}, expect{remaining: []string{"b", "c"}}},
		{"wrong schedule cannot delete", input{"a", "s2", true, false}, expect{failure: true, remaining: []string{"a", "b", "c"}}},
		{"missing expected schedule rejected", input{id: "a", trusted: true}, expect{failure: true, remaining: []string{"a", "b", "c"}}},
		{"untrusted delete rejected", input{id: "a", expectedSchedule: "s1"}, expect{failure: true, remaining: []string{"a", "b", "c"}}},
		{"insert fills status kind and timestamp", input{id: "new", trusted: true, insert: true}, expect{remaining: []string{"a", "b", "c", "new"}}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			db, _ := legacyScheduleRunFixture(t, project)
			rt, _, key := legacyScheduleRunRuntime(t, db, tc.input.trusted, nil)
			item := &write.LegacyRun{}
			item.SetId(tc.input.id)
			if tc.input.insert {
				item.SetScheduleId("s1")
			} else {
				item.SetShouldDelete(true)
			}
			mutation := &write.Input{}
			if tc.input.expectedSchedule != "" {
				mutation.SetExpectedScheduleID(tc.input.expectedSchedule)
			}
			mutation.SetRuns([]*write.LegacyRun{item})
			err := invokeLegacyScheduleWriter(context.Background(), rt, key, mutation)
			if (err != nil) != tc.expect.failure {
				t.Fatalf("mutation error=%v expected failure=%v", err, tc.expect.failure)
			}
			if rows := legacyScheduleRows(t, db); !reflect.DeepEqual(rows, tc.expect.remaining) {
				t.Fatalf("rows=%v expected=%v", rows, tc.expect.remaining)
			}
			if tc.input.insert {
				var status, kind string
				var created time.Time
				must(t, db.QueryRow("SELECT status,conversation_kind,created_at FROM schedule_run WHERE id='new'").Scan(&status, &kind, &created))
				if status != "pending" || kind != "scheduled" || created.IsZero() {
					t.Fatalf("defaults=(%s,%s,%s)", status, kind, created)
				}
			}
		})
	}
}

func TestLegacyScheduleRunReaderScope(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type input struct {
		schedule, conversation, status string
		trusted                        bool
	}
	type useCase struct {
		desc   string
		input  input
		expect []string
	}
	for _, tc := range []useCase{
		{"conversation filter", input{conversation: "c1", trusted: true}, []string{"a", "b"}},
		{"schedule and status intersect", input{schedule: "s1", status: "failed", trusted: true}, []string{"b"}},
		{"unknown schedule empty", input{schedule: "missing", trusted: true}, []string{}},
		{"untrusted access empty", input{conversation: "c1"}, []string{}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			db, _ := legacyScheduleRunFixture(t, project)
			rt, key, _ := legacyScheduleRunRuntime(t, db, tc.input.trusted, nil)
			query := &read.Input{}
			if tc.input.schedule != "" {
				query.SetScheduleID(tc.input.schedule)
			}
			if tc.input.conversation != "" {
				query.SetConversationID(tc.input.conversation)
			}
			if tc.input.status != "" {
				query.SetStatus(tc.input.status)
			}
			rows, err := invokeLegacyScheduleReader(context.Background(), rt, key, query)
			must(t, err)
			ids := []string{}
			for _, row := range rows {
				ids = append(ids, row.Id)
			}
			if !reflect.DeepEqual(ids, tc.expect) {
				t.Fatalf("ids=%v expected=%v", ids, tc.expect)
			}
		})
	}
	t.Run("HTTP query cannot replace trusted access", func(t *testing.T) {
		db, _ := legacyScheduleRunFixture(t, project)
		rt, _, _ := legacyScheduleRunRuntime(t, db, false, nil)
		request := httptest.NewRequest("GET", "/v1/internal/agently/scheduler/legacy-run?conversationId=c1&trusted=true", nil)
		scope, err := requestprovider.New(request)
		must(t, err)
		defer scope.Close()
		value, err := rt.ExecuteRoute(context.Background(), "GET", "/v1/internal/agently/scheduler/legacy-run", scope)
		must(t, err)
		if len(value.(*read.Output).Data) != 0 {
			t.Fatal("HTTP query replaced host trust")
		}
	})
}

func TestLegacyScheduleRunCallerTransaction(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type useCase struct {
		desc   string
		commit bool
		expect int
	}
	for _, tc := range []useCase{{"caller rollback", false, 1}, {"caller commit", true, 0}} {
		t.Run(tc.desc, func(t *testing.T) {
			db, _ := legacyScheduleRunFixture(t, project)
			db.SetMaxOpenConns(1)
			tx, err := db.BeginTx(context.Background(), nil)
			must(t, err)
			defer tx.Rollback()
			rt, _, key := legacyScheduleRunRuntime(t, db, true, tx)
			item := &write.LegacyRun{}
			item.SetId("a")
			item.SetShouldDelete(true)
			input := &write.Input{}
			input.SetExpectedScheduleID("s1")
			input.SetRuns([]*write.LegacyRun{item})
			must(t, invokeLegacyScheduleWriter(context.Background(), rt, key, input))
			var pending int
			must(t, tx.QueryRow("SELECT COUNT(*) FROM schedule_run WHERE id='a'").Scan(&pending))
			if pending != 0 {
				t.Fatalf("pending count=%d", pending)
			}
			if tc.commit {
				must(t, tx.Commit())
			} else {
				must(t, tx.Rollback())
			}
			var stored int
			must(t, db.QueryRow("SELECT COUNT(*) FROM schedule_run WHERE id='a'").Scan(&stored))
			if stored != tc.expect {
				t.Fatalf("stored count=%d expected=%d", stored, tc.expect)
			}
		})
	}
}
