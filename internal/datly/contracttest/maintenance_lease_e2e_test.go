package tests

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"runtime"
	"sync"
	"testing"
	"time"

	read "github.com/viant/agently-core/internal/datly/maintenancelease/read"
	write "github.com/viant/agently-core/internal/datly/maintenancelease/write"
	maintenance "github.com/viant/agently-core/internal/store/maintenancelease"

	"github.com/viant/agently-core/internal/datly/dbtime"
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

func TestMaintenanceLeasePurgeRoutePrivate(t *testing.T) {
	holder := reflect.TypeFor[maintenance.PurgeComponent]()
	field, ok := holder.FieldByName("Contract")
	if !ok {
		t.Fatal("purge contract is missing")
	}
	tag, present, err := dtag.ParseComponent(field.Tag)
	must(t, err)
	if !present {
		t.Fatal("purge component tag is missing")
	}
	source := &bootstrap.RouteSource{HolderType: holder.Name(), FieldName: field.Name, PackageName: "maintenancelease", PackagePath: holder.PkgPath(), Tag: tag, InputType: "PurgeInput", OutputType: "PurgeOutput"}
	component, err := source.Resolve(reflect.TypeFor[maintenance.PurgeInput](), reflect.TypeFor[maintenance.PurgeOutput]())
	must(t, err)
	if len(component.Routes) != 1 || !component.Routes[0].Internal {
		t.Fatalf("purge route is public: %+v", component.Routes)
	}
}

func maintenanceLeaseRuntime(t *testing.T, db *sql.DB, supplied ...*sql.Tx) (*druntime.Runtime, spec.Key, spec.Key) {
	t.Helper()
	resources := resource.New()
	must(t, resources.Register(read.ReaderDatlyResourceNamespace, read.ReaderDatlyResources))
	must(t, resources.Register(write.WriterDatlyResourceNamespace, write.WriterDatlyResources))
	r := payloadArtifact(t, resources, reflect.TypeFor[read.ReaderComponent](), reflect.TypeFor[read.Input](), reflect.TypeFor[read.Output]())
	w := payloadArtifact(t, resources, reflect.TypeFor[write.WriterComponent](), reflect.TypeFor[write.Input](), reflect.TypeFor[write.Output]())
	parent := payloadArtifact(t, resources, reflect.TypeFor[maintenance.PurgeComponent](), reflect.TypeFor[maintenance.PurgeInput](), reflect.TypeFor[maintenance.PurgeOutput]())
	customHandler, err := (maintenance.PurgeComponent{}).DatlyHandler("NewPurge")()
	must(t, err)
	builder, err := bootstrap.NewArtifactBuilder(nil)
	must(t, err)
	parent, err = builder.Build(bootstrap.ArtifactInput{Component: parent.Component, InputType: reflect.TypeFor[maintenance.PurgeInput](), OutputType: reflect.TypeFor[maintenance.PurgeOutput](), Resources: resources, Handler: customHandler})
	must(t, err)
	var tx *sql.Tx
	if len(supplied) > 0 {
		tx = supplied[0]
	}
	parentRegistration, err := parent.Registration(registry.RegisteredComponent{DataSource: dml.Source{DB: db, Tx: tx}})
	must(t, err)
	reader, err := r.ReaderCompilation().NewExecution(bootstrap.ReaderRuntimeConfig{SQL: &dsql.SQLComponent{DB: db}})
	must(t, err)
	views, err := viewprovider.New(viewprovider.Config{Dependencies: w.ViewDependencies, Input: w.Input, SQL: &dsql.SQLComponent{DB: db}})
	must(t, err)
	handler, err := writer.New(w.Component, reflect.TypeFor[write.Input](), reflect.TypeFor[write.Output](), "patch")
	must(t, err)
	access := ordinaryAccess("maintenanceaccess", func(context.Context, reflect.Type, string) (any, bool, error) { return true, true, nil })
	rt, err := druntime.NewRuntime([]*registry.RegisteredComponent{
		{Component: r.Component, Input: r.Input, Output: r.Output, OutputType: reflect.TypeFor[read.Output](), Reader: reader, Providers: []locator.Provider{access}},
		{Component: w.Component, Input: w.Input, Output: w.Output, OutputType: reflect.TypeFor[write.Output](), Handler: handler, Providers: []locator.Provider{access, views}, DataSource: dml.Source{DB: db}},
		parentRegistration,
	}, druntime.WithResources(resources))
	must(t, err)
	return rt, r.Component.Key, w.Component.Key
}

func TestMaintenanceLeaseReaderWriter(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	db, _ := goalFixture(t, filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file)))))
	rt, rkey, wkey := maintenanceLeaseRuntime(t, db)
	ctx := context.Background()
	readRows := func(input *read.Input) []*read.LeaseSnapshot {
		t.Helper()
		value, err := rt.InvokeComponent(ctx, dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: rkey, Route: spec.RouteRef{Method: "GET", Path: "/v1/internal/agently/maintenance-lease"}}, Input: input})
		must(t, err)
		return value.(*read.Output).Data
	}
	patch := func(input *write.Input) error {
		_, err := rt.InvokeComponent(ctx, dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: wkey, Route: spec.RouteRef{Method: "PATCH", Path: "/v1/internal/agently/maintenance-lease"}}, Input: input})
		return err
	}
	clock := &read.Input{}
	clock.SetMode("clock")
	rows := readRows(clock)
	if len(rows) != 1 || rows[0].LeaseKey != nil {
		t.Fatalf("empty clock reader returned %+v", rows)
	}
	now, err := dbtime.ParseDatabaseUTC(rows[0].DbNow)
	must(t, err)
	create := &write.Lease{}
	create.SetLeaseKey("cleanup")
	create.SetOwnerId("worker-a")
	create.SetLeaseToken("token-a")
	create.SetLeaseUntil(now.Add(time.Hour))
	create.SetCreatedAt(now)
	create.SetUpdatedAt(now)
	in := &write.Input{}
	in.SetMode("create")
	in.SetLeases([]*write.Lease{create})
	must(t, patch(in))
	lookup := &read.Input{}
	lookup.SetLeaseKey("cleanup")
	rows = readRows(lookup)
	if len(rows) != 1 || rows[0].LeaseToken == nil || *rows[0].LeaseToken != "token-a" {
		t.Fatalf("created lease not readable: %+v", rows)
	}
	claim := &write.Lease{}
	claim.SetLeaseKey("cleanup")
	claim.SetOwnerId("worker-b")
	claim.SetLeaseToken("token-b")
	claim.SetLeaseUntil(now.Add(2 * time.Hour))
	claim.SetUpdatedAt(now)
	in = &write.Input{}
	in.SetMode("claim")
	in.SetExpectedToken("token-a")
	in.SetExpiresBefore(now)
	in.SetLeases([]*write.Lease{claim})
	if err := patch(in); err == nil {
		t.Fatal("live lease claim unexpectedly succeeded")
	}
	_, err = db.Exec("UPDATE maintenance_lease SET lease_until=? WHERE lease_key='cleanup'", now.Add(-time.Minute))
	must(t, err)
	must(t, patch(in))
	rows = readRows(lookup)
	if rows[0].OwnerId == nil || *rows[0].OwnerId != "worker-b" {
		t.Fatalf("expired claim owner=%+v", rows[0])
	}
	renew := &write.Lease{}
	renew.SetLeaseKey("cleanup")
	renew.SetLeaseUntil(now.Add(3 * time.Hour))
	renew.SetUpdatedAt(now)
	in = &write.Input{}
	in.SetMode("renew")
	in.SetExpectedOwner("worker-b")
	in.SetExpectedToken("token-b")
	in.SetLiveAfter(now)
	in.SetLeases([]*write.Lease{renew})
	must(t, patch(in))
	in.SetExpectedToken("stale-token")
	if err := patch(in); err == nil {
		t.Fatal("stale token renewed lease")
	}
	release := &write.Lease{}
	release.SetLeaseKey("cleanup")
	release.SetLeaseUntil(now)
	release.SetUpdatedAt(now)
	in = &write.Input{}
	in.SetMode("release")
	in.SetExpectedOwner("worker-b")
	in.SetExpectedToken("token-b")
	in.SetLeases([]*write.Lease{release})
	must(t, patch(in))
	_, err = db.Exec("UPDATE maintenance_lease SET lease_until=? WHERE lease_key='cleanup'", now.Add(-8*24*time.Hour))
	must(t, err)
	remove := &write.Lease{}
	remove.SetLeaseKey("cleanup")
	remove.SetShouldDelete(true)
	in = &write.Input{}
	in.SetMode("delete")
	in.SetExpectedToken("token-b")
	in.SetExpiresBefore(now.Add(-7 * 24 * time.Hour))
	in.SetLeases([]*write.Lease{remove})
	must(t, patch(in))
	rows = readRows(lookup)
	if len(rows) != 1 || rows[0].LeaseKey != nil {
		t.Fatalf("deleted lease still visible: %+v", rows)
	}
}

func TestMaintenanceLeaseStoreLifecycle(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	db, _ := goalFixture(t, filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file)))))
	rt, _, _ := maintenanceLeaseRuntime(t, db)
	store := &maintenance.Store{Invoker: rt}
	ctx := context.Background()
	first, err := store.Acquire(ctx, "maintenance", "worker-a", time.Hour)
	must(t, err)
	if !first.Acquired || first.Lease.Token == "" {
		t.Fatalf("first acquisition=%+v", first)
	}
	blocked, err := store.Acquire(ctx, "maintenance", "worker-b", time.Hour)
	must(t, err)
	if blocked.Acquired || blocked.Lease.OwnerID != "worker-a" || blocked.Lease.Token != "" {
		t.Fatalf("active holder leaked token or was replaced: %+v", blocked)
	}
	_, err = db.Exec("UPDATE maintenance_lease SET lease_until=? WHERE lease_key='maintenance'", time.Now().UTC().Add(-time.Minute))
	must(t, err)
	second, err := store.Acquire(ctx, "maintenance", "worker-b", time.Hour)
	must(t, err)
	if !second.Acquired || second.Lease.Token == first.Lease.Token {
		t.Fatalf("expired lease was not fenced: %+v", second)
	}
	renewed, _, err := store.Renew(ctx, first.Lease, time.Hour)
	must(t, err)
	if renewed {
		t.Fatal("old token renewed a new owner's lease")
	}
	renewed, until, err := store.Renew(ctx, second.Lease, time.Hour)
	must(t, err)
	if !renewed || until.IsZero() {
		t.Fatalf("current holder renewal=(%t,%v)", renewed, until)
	}
	released, err := store.Release(ctx, first.Lease)
	must(t, err)
	if released {
		t.Fatal("old token released a new owner's lease")
	}
	released, err = store.Release(ctx, second.Lease)
	must(t, err)
	if !released {
		t.Fatal("current owner could not release")
	}
}

func TestMaintenanceLeaseIndependentConnections(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	db, path := goalFixture(t, filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file)))))
	_, err := db.Exec("PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000")
	must(t, err)
	other, err := sql.Open("sqlite3", path+"?_foreign_keys=on&_busy_timeout=5000")
	must(t, err)
	defer other.Close()
	other.SetMaxOpenConns(1)
	firstRT, _, _ := maintenanceLeaseRuntime(t, db)
	secondRT, _, _ := maintenanceLeaseRuntime(t, other)
	stores := []*maintenance.Store{{Invoker: firstRT}, {Invoker: secondRT}}
	start := make(chan struct{})
	results := make([]*maintenance.AcquireResult, 2)
	errors := make([]error, 2)
	var group sync.WaitGroup
	for i := range stores {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			<-start
			results[index], errors[index] = stores[index].Acquire(context.Background(), "race", "worker-"+string(rune('a'+index)), time.Hour)
		}(i)
	}
	close(start)
	group.Wait()
	for _, err := range errors {
		must(t, err)
	}
	winners := 0
	for _, result := range results {
		if result.Acquired {
			winners++
		} else if result.Lease.Token != "" {
			t.Fatal("loser saw fencing token")
		}
	}
	if winners != 1 {
		t.Fatalf("acquisition winners=%d results=%+v", winners, results)
	}
}

func TestMaintenanceLeasePurgeManagedTransaction(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	for _, tc := range []struct {
		name        string
		reject      bool
		stale       bool
		wantDeleted int64
		wantOld     int
	}{
		{name: "deletes expired rows", wantDeleted: 2, wantOld: 0},
		{name: "late deletion rolls back every row", reject: true, wantOld: 2},
		{name: "lost lease cannot delete", stale: true, wantOld: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, _ := goalFixture(t, project)
			db.SetMaxOpenConns(1)
			old := time.Now().UTC().Add(-8 * 24 * time.Hour)
			recent := time.Now().UTC().Add(-time.Hour)
			_, err := db.Exec(`INSERT INTO maintenance_lease(lease_key,owner_id,lease_token,lease_until) VALUES
 ('old-a','old','token-a',?),('old-b','old','token-b',?),('recent','other','token-recent',?)`, old, old, recent)
			must(t, err)
			if tc.reject {
				_, err = db.Exec("CREATE TRIGGER reject_old_b BEFORE DELETE ON maintenance_lease WHEN OLD.lease_key='old-b' BEGIN SELECT RAISE(ABORT,'late purge failure'); END")
				must(t, err)
			}
			rt, _, _ := maintenanceLeaseRuntime(t, db)
			store := &maintenance.Store{Invoker: rt}
			held, err := store.Acquire(context.Background(), "held", "worker", time.Hour)
			must(t, err)
			var beforeGuard time.Time
			must(t, db.QueryRow("SELECT updated_at FROM maintenance_lease WHERE lease_key='held'").Scan(&beforeGuard))
			lease := held.Lease
			if tc.stale {
				lease.Token = "stale-token"
			}
			deleted, err := store.DeleteExpired(context.Background(), lease)
			if tc.stale && !errors.Is(err, maintenance.ErrLeaseLost) {
				t.Fatalf("stale purge error=%v", err)
			}
			if tc.reject && err == nil {
				t.Fatal("late trigger did not reject purge")
			}
			if !tc.reject && !tc.stale {
				must(t, err)
			}
			if !tc.reject && !tc.stale && deleted != tc.wantDeleted {
				t.Fatalf("deleted=%d want=%d", deleted, tc.wantDeleted)
			}
			var oldCount, recentCount, heldCount int
			must(t, db.QueryRow("SELECT COUNT(*) FROM maintenance_lease WHERE lease_key IN ('old-a','old-b')").Scan(&oldCount))
			must(t, db.QueryRow("SELECT COUNT(*) FROM maintenance_lease WHERE lease_key='recent'").Scan(&recentCount))
			must(t, db.QueryRow("SELECT COUNT(*) FROM maintenance_lease WHERE lease_key='held'").Scan(&heldCount))
			if oldCount != tc.wantOld || recentCount != 1 || heldCount != 1 {
				t.Fatalf("remaining old=%d recent=%d held=%d", oldCount, recentCount, heldCount)
			}
			var afterGuard time.Time
			must(t, db.QueryRow("SELECT updated_at FROM maintenance_lease WHERE lease_key='held'").Scan(&afterGuard))
			if tc.reject || tc.stale {
				if !afterGuard.Equal(beforeGuard) {
					t.Fatalf("failed purge changed held lease timestamp: %v -> %v", beforeGuard, afterGuard)
				}
			} else if afterGuard.Equal(beforeGuard) {
				t.Fatal("successful purge did not acquire a write guard")
			}
		})
	}
}

func TestMaintenanceLeasePurgeCallerTransaction(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	db, _ := goalFixture(t, filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file)))))
	db.SetMaxOpenConns(1)
	baseRT, _, _ := maintenanceLeaseRuntime(t, db)
	held, err := (&maintenance.Store{Invoker: baseRT}).Acquire(context.Background(), "held", "worker", time.Hour)
	must(t, err)
	_, err = db.Exec("INSERT INTO maintenance_lease(lease_key,owner_id,lease_token,lease_until) VALUES('old','other','old-token',?)", time.Now().UTC().Add(-8*24*time.Hour))
	must(t, err)
	tx, err := db.BeginTx(context.Background(), nil)
	must(t, err)
	defer tx.Rollback()
	rt, _, _ := maintenanceLeaseRuntime(t, db, tx)
	deleted, err := (&maintenance.Store{Invoker: rt}).DeleteExpired(context.Background(), held.Lease)
	must(t, err)
	if deleted != 1 {
		t.Fatalf("caller-pending deleted=%d", deleted)
	}
	var pending int
	must(t, tx.QueryRow("SELECT COUNT(*) FROM maintenance_lease WHERE lease_key='old'").Scan(&pending))
	if pending != 0 {
		t.Fatalf("old row remains inside caller transaction: %d", pending)
	}
	must(t, tx.Rollback())
	var persisted int
	must(t, db.QueryRow("SELECT COUNT(*) FROM maintenance_lease WHERE lease_key='old'").Scan(&persisted))
	if persisted != 1 {
		t.Fatalf("caller rollback lost old row: %d", persisted)
	}
}
