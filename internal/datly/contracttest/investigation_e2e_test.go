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

	read "github.com/viant/agently-core/internal/datly/investigation/read"
	write "github.com/viant/agently-core/internal/datly/investigation/write"
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

func investigationFixture(t *testing.T, project string) *sql.DB {
	t.Helper()
	db, _ := goalFixture(t, project)
	_, err := db.Exec(`INSERT INTO investigation(id,title,created_by,conversation_id,created) VALUES
 ('a','One','u1','c1','2026-01-01 01:00:00'),
 ('b','Two','u1','c1','2026-01-01 02:00:00'),
 ('c','Three','u2','c2','2026-01-01 03:00:00')`)
	must(t, err)
	return db
}

func investigationRuntime(t *testing.T, db *sql.DB, trusted bool, supplied *sql.Tx) (*druntime.Runtime, spec.Key, spec.Key) {
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
	access := ordinaryAccess("investigationaccess", func(context.Context, reflect.Type, string) (any, bool, error) { return trusted, true, nil })
	rt, err := druntime.NewRuntime([]*registry.RegisteredComponent{
		{Component: r.Component, Input: r.Input, Output: r.Output, OutputType: reflect.TypeFor[read.Output](), Reader: reader, Providers: []locator.Provider{access}},
		{Component: w.Component, Input: w.Input, Output: w.Output, OutputType: reflect.TypeFor[write.Output](), Handler: handler, Providers: []locator.Provider{access, views}, DataSource: dml.Source{DB: db, Tx: supplied}},
	}, druntime.WithResources(resources))
	must(t, err)
	return rt, r.Component.Key, w.Component.Key
}

func investigationRows(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query("SELECT id FROM investigation ORDER BY id")
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

func investigationRead(ctx context.Context, rt *druntime.Runtime, key spec.Key, input *read.Input) ([]*read.Investigation, error) {
	value, err := rt.InvokeComponent(ctx, dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "GET", Path: "/v1/internal/agently/investigation"}}, Input: input})
	if err != nil {
		return nil, err
	}
	return value.(*read.Output).Data, nil
}

func investigationWrite(ctx context.Context, rt *druntime.Runtime, key spec.Key, input *write.Input) error {
	_, err := rt.InvokeComponent(ctx, dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: key, Route: spec.RouteRef{Method: "PATCH", Path: "/v1/internal/agently/investigation"}}, Input: input})
	return err
}

func TestInvestigationLegacyDeletionParity(t *testing.T) {
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
		{"delete attached investigations", input{conversation: "c1"}, expect{remaining: []string{"c"}}},
		{"missing conversation no-op", input{conversation: "missing"}, expect{remaining: []string{"a", "b", "c"}}},
		{"late failure rolls back whole batch", input{conversation: "c1", reject: true}, expect{failure: true, remaining: []string{"a", "b", "c"}}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			oldDB, db := investigationFixture(t, project), investigationFixture(t, project)
			if tc.input.reject {
				for _, fixture := range []*sql.DB{oldDB, db} {
					_, err := fixture.Exec("CREATE TRIGGER reject_investigation_b BEFORE DELETE ON investigation WHEN OLD.id='b' BEGIN SELECT RAISE(ABORT,'fixture late investigation rejection'); END")
					must(t, err)
				}
			}
			_, oldErr := oldDB.Exec("DELETE FROM investigation WHERE conversation_id=?", tc.input.conversation)
			rt, rkey, wkey := investigationRuntime(t, db, true, nil)
			query := &read.Input{}
			query.SetConversationID(tc.input.conversation)
			rows, err := investigationRead(context.Background(), rt, rkey, query)
			must(t, err)
			deletions := make([]*write.Investigation, 0, len(rows))
			for _, row := range rows {
				item := &write.Investigation{}
				item.SetId(row.Id)
				item.SetShouldDelete(true)
				deletions = append(deletions, item)
			}
			mutation := &write.Input{}
			mutation.SetExpectedConversationID(tc.input.conversation)
			mutation.SetInvestigations(deletions)
			err = investigationWrite(context.Background(), rt, wkey, mutation)
			if (oldErr != nil) != tc.expect.failure || (err != nil) != tc.expect.failure {
				t.Fatalf("legacy=%v native=%v expected failure=%v", oldErr, err, tc.expect.failure)
			}
			if oldRows, newRows := investigationRows(t, oldDB), investigationRows(t, db); !reflect.DeepEqual(oldRows, newRows) || !reflect.DeepEqual(newRows, tc.expect.remaining) {
				t.Fatalf("legacy=%v native=%v expected=%v", oldRows, newRows, tc.expect.remaining)
			}
		})
	}
}

func TestInvestigationGuardAndDefaults(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type input struct {
		id, expected    string
		trusted, insert bool
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
		{"matching conversation deletes", input{id: "a", expected: "c1", trusted: true}, expect{remaining: []string{"b", "c"}}},
		{"wrong conversation cannot delete", input{id: "a", expected: "c2", trusted: true}, expect{failure: true, remaining: []string{"a", "b", "c"}}},
		{"missing expected conversation rejected", input{id: "a", trusted: true}, expect{failure: true, remaining: []string{"a", "b", "c"}}},
		{"untrusted deletion rejected", input{id: "a", expected: "c1"}, expect{failure: true, remaining: []string{"a", "b", "c"}}},
		{"insert fills created", input{id: "new", trusted: true, insert: true}, expect{remaining: []string{"a", "b", "c", "new"}}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			db := investigationFixture(t, project)
			rt, _, key := investigationRuntime(t, db, tc.input.trusted, nil)
			item := &write.Investigation{}
			item.SetId(tc.input.id)
			if tc.input.insert {
				conversation := "c1"
				item.SetConversationId(&conversation)
			} else {
				item.SetShouldDelete(true)
			}
			mutation := &write.Input{}
			if tc.input.expected != "" {
				mutation.SetExpectedConversationID(tc.input.expected)
			}
			mutation.SetInvestigations([]*write.Investigation{item})
			err := investigationWrite(context.Background(), rt, key, mutation)
			if (err != nil) != tc.expect.failure {
				t.Fatalf("mutation error=%v expected failure=%v", err, tc.expect.failure)
			}
			if rows := investigationRows(t, db); !reflect.DeepEqual(rows, tc.expect.remaining) {
				t.Fatalf("rows=%v expected=%v", rows, tc.expect.remaining)
			}
			if tc.input.insert {
				var created time.Time
				must(t, db.QueryRow("SELECT created FROM investigation WHERE id='new'").Scan(&created))
				if created.IsZero() {
					t.Fatal("created default is zero")
				}
			}
		})
	}
}

func TestInvestigationReaderScope(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type input struct {
		conversation, actor string
		trusted             bool
	}
	type useCase struct {
		desc   string
		input  input
		expect []string
	}
	for _, tc := range []useCase{
		{"conversation predicate", input{conversation: "c1", trusted: true}, []string{"a", "b"}},
		{"actor predicate", input{actor: "u2", trusted: true}, []string{"c"}},
		{"predicate intersection", input{conversation: "c1", actor: "u2", trusted: true}, []string{}},
		{"untrusted has no rows", input{conversation: "c1"}, []string{}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			db := investigationFixture(t, project)
			rt, key, _ := investigationRuntime(t, db, tc.input.trusted, nil)
			query := &read.Input{}
			if tc.input.conversation != "" {
				query.SetConversationID(tc.input.conversation)
			}
			if tc.input.actor != "" {
				query.SetCreatedBy(tc.input.actor)
			}
			rows, err := investigationRead(context.Background(), rt, key, query)
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
		db := investigationFixture(t, project)
		rt, _, _ := investigationRuntime(t, db, false, nil)
		request := httptest.NewRequest("GET", "/v1/internal/agently/investigation?conversationId=c1&trusted=true", nil)
		scope, err := requestprovider.New(request)
		must(t, err)
		defer scope.Close()
		value, err := rt.ExecuteRoute(context.Background(), "GET", "/v1/internal/agently/investigation", scope)
		must(t, err)
		if len(value.(*read.Output).Data) != 0 {
			t.Fatal("HTTP query replaced host trust")
		}
	})
}

func TestInvestigationCallerTransaction(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type useCase struct {
		desc   string
		commit bool
		expect int
	}
	for _, tc := range []useCase{{"caller rollback", false, 1}, {"caller commit", true, 0}} {
		t.Run(tc.desc, func(t *testing.T) {
			db := investigationFixture(t, project)
			db.SetMaxOpenConns(1)
			tx, err := db.BeginTx(context.Background(), nil)
			must(t, err)
			defer tx.Rollback()
			rt, _, key := investigationRuntime(t, db, true, tx)
			item := &write.Investigation{}
			item.SetId("a")
			item.SetShouldDelete(true)
			input := &write.Input{}
			input.SetExpectedConversationID("c1")
			input.SetInvestigations([]*write.Investigation{item})
			must(t, investigationWrite(context.Background(), rt, key, input))
			var pending int
			must(t, tx.QueryRow("SELECT COUNT(*) FROM investigation WHERE id='a'").Scan(&pending))
			if pending != 0 {
				t.Fatalf("pending count=%d", pending)
			}
			if tc.commit {
				must(t, tx.Commit())
			} else {
				must(t, tx.Rollback())
			}
			var stored int
			must(t, db.QueryRow("SELECT COUNT(*) FROM investigation WHERE id='a'").Scan(&stored))
			if stored != tc.expect {
				t.Fatalf("stored count=%d expected=%d", stored, tc.expect)
			}
		})
	}
}
