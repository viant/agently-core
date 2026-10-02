package tests

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	read "github.com/viant/agently-core/internal/datly/reporting/sharedartifact/read"
	write "github.com/viant/agently-core/internal/datly/reporting/sharedartifact/write"
	shared "github.com/viant/agently-core/internal/store/reporting/sharedartifact"
	"github.com/viant/bindly/locator"
	"github.com/viant/bindly/resource"
	"github.com/viant/datly/bootstrap"
	druntime "github.com/viant/datly/runtime"
	writer "github.com/viant/datly/runtime/handler/writer"
	"github.com/viant/datly/runtime/registry"
	dsql "github.com/viant/datly/sql"
	"github.com/viant/datly/sql/dml"
	viewprovider "github.com/viant/datly/sql/reader/provider"
)

func forgeSharedStoreRuntime(t *testing.T, db *sql.DB, owner string) *shared.Store {
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
	rt, err := druntime.NewRuntime([]*registry.RegisteredComponent{
		{Component: r.Component, Input: r.Input, Output: r.Output, OutputType: reflect.TypeFor[read.Output](), Reader: reader},
		{Component: w.Component, Input: w.Input, Output: w.Output, OutputType: reflect.TypeFor[write.Output](), Handler: handler, Providers: []locator.Provider{views}, DataSource: dml.Source{DB: db}},
	}, druntime.WithResources(resources))
	must(t, err)
	return &shared.Store{Invoker: rt, OwnerID: func(context.Context) string { return owner }}
}

func TestForgeSharedArtifactStoreLifecycle(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	type input struct {
		owner, action, id string
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
		{"owner creates new", input{"u1", "create", "new"}, expect{count: 1, title: "new title"}},
		{"duplicate create fails", input{"u1", "create", "existing"}, expect{failure: shared.ErrAlreadyExists, count: 1, title: "old"}},
		{"owner updates existing", input{"u1", "update", "existing"}, expect{count: 1, title: "new title"}},
		{"unknown update fails", input{"u1", "update", "new"}, expect{failure: shared.ErrNotFound}},
		{"owner deletes existing", input{"u1", "delete", "existing"}, expect{}},
		{"unknown delete fails", input{"u1", "delete", "new"}, expect{failure: shared.ErrNotFound}},
		{"foreign update fails", input{"u2", "update", "existing"}, expect{failure: shared.ErrNotFound, count: 1, title: "old"}},
		{"foreign delete fails", input{"u2", "delete", "existing"}, expect{failure: shared.ErrNotFound, count: 1, title: "old"}},
		{"anonymous list is empty", input{"", "list", "existing"}, expect{count: 1, title: "old"}},
		{"owner list is isolated", input{"u1", "list", "existing"}, expect{count: 1, title: "old"}},
		{"foreign get fails", input{"u2", "get", "existing"}, expect{failure: shared.ErrNotFound, count: 1, title: "old"}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			db, _ := forgeFixture(t, project)
			store := forgeSharedStoreRuntime(t, db, tc.input.owner)
			var err error
			switch tc.input.action {
			case "create", "update":
				record := &shared.Record{ArtifactID: tc.input.id, ArtifactRef: "report://new", OwnerID: tc.input.owner, Kind: "report", Lifecycle: "saved", Title: "new title", Document: []byte("{}")}
				if tc.input.action == "create" {
					err = store.Create(context.Background(), record)
				} else {
					err = store.Update(context.Background(), record)
				}
			case "delete":
				err = store.Delete(context.Background(), tc.input.id)
			case "get":
				_, err = store.Get(context.Background(), tc.input.id)
			case "list":
				var rows []*shared.Record
				rows, err = store.List(context.Background())
				if err == nil {
					want := 1
					if tc.input.owner == "" {
						want = 0
					}
					if len(rows) != want {
						t.Fatalf("list rows = %d, want %d", len(rows), want)
					}
				}
			}
			if tc.expect.failure == nil && err != nil {
				t.Fatalf("unexpected %s failure: %v", tc.input.action, err)
			}
			if tc.expect.failure != nil && !errors.Is(err, tc.expect.failure) {
				t.Fatalf("%s failure = %v, want %v", tc.input.action, err, tc.expect.failure)
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
