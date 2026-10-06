package native_test

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/stretchr/testify/require"
	deleteview "github.com/viant/agently-core/internal/datly/run/delete"
	runstore "github.com/viant/agently-core/internal/store/agentrun"
	"github.com/viant/bindly/locator"
	"github.com/viant/bindly/resource"
	"github.com/viant/datly/bootstrap"
	dexec "github.com/viant/datly/exec"
	druntime "github.com/viant/datly/runtime"
	"github.com/viant/datly/runtime/handler/writer"
	"github.com/viant/datly/runtime/registry"
	"github.com/viant/datly/spec"
	dsql "github.com/viant/datly/sql"
	"github.com/viant/datly/sql/dml"
	viewprovider "github.com/viant/datly/sql/reader/provider"
	dtag "github.com/viant/datly/tag"
	"reflect"
	"testing"
)

func TestRunKeyDeleteSQLite(t *testing.T) {
	s, _, db := orphanFixture(t)
	runKeyDelete(t, s.Invoker, db, "run-key-sqlite-", true)
}
func TestRunKeyDeleteMySQL(t *testing.T) {
	s, _, db, p := orphanMySQLFixture(t)
	runKeyDelete(t, s.Invoker, db, p+"run-key-", false)
}
func runKeyDelete(t *testing.T, invoker dexec.ComponentInvoker, db *sql.DB, p string, sqlite bool) {
	ctx := context.Background()
	for _, suffix := range []string{"delete", "rollback"} {
		_, err := db.Exec("INSERT INTO run(id,status,conversation_kind,attempt,iteration,created_at) VALUES(?,'failed','interactive',0,0,CURRENT_TIMESTAMP)", p+suffix)
		require.NoError(t, err)
	}
	if sqlite {
		_, err := db.Exec("UPDATE run SET last_heartbeat_at='malformed' WHERE id=?", p+"delete")
		require.NoError(t, err)
	}
	require.NoError(t, (&runstore.Store{Invoker: invoker}).DeleteTrusted(ctx, p+"delete", p+"missing"))
	var count int
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM run WHERE id=?", p+"delete").Scan(&count))
	require.Zero(t, count)
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer tx.Rollback()
	runtime := runDeleteCallerRuntime(t, db, tx)
	input := &deleteview.Input{}
	row := &deleteview.RunDelete{}
	row.SetId(p + "rollback")
	row.SetShouldDelete(true)
	input.SetRuns([]*deleteview.RunDelete{row})
	_, err = runtime.InvokeComponent(ctx, dexec.ComponentRequest{Target: runKeyTarget(), Input: input})
	require.NoError(t, err)
	require.NoError(t, tx.QueryRow("SELECT COUNT(*) FROM run WHERE id=?", row.Id).Scan(&count))
	require.Zero(t, count)
	require.NoError(t, tx.Rollback())
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM run WHERE id=?", row.Id).Scan(&count))
	require.Equal(t, 1, count, "writer must retain caller completion ownership")
	invalid := &deleteview.RunDelete{}
	invalid.SetId(p + "rollback")
	invalid.SetShouldDelete(false)
	input = &deleteview.Input{}
	input.SetRuns([]*deleteview.RunDelete{invalid})
	_, err = invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: runKeyTarget(), Input: input})
	require.ErrorContains(t, err, "marked identity and deletion")
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM run WHERE id=?", invalid.Id).Scan(&count))
	require.Equal(t, 1, count)
}
func runKeyTarget() dexec.ComponentTarget {
	return dexec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[deleteview.WriterComponent]().PkgPath(), Name: "writer"}, Route: spec.RouteRef{Method: "PATCH", Path: "/v1/internal/agently/run/delete"}}
}
func runDeleteCallerRuntime(t *testing.T, db *sql.DB, tx *sql.Tx) *druntime.Runtime {
	t.Helper()
	typ := reflect.TypeFor[deleteview.WriterComponent]()
	field, _ := typ.FieldByName("Contract")
	metadata, _, err := dtag.ParseComponent(field.Tag)
	require.NoError(t, err)
	source := &bootstrap.RouteSource{PackagePath: typ.PkgPath(), HolderType: typ.Name(), FieldName: field.Name, Tag: metadata, InputType: "Input", OutputType: "Output"}
	component, err := source.Resolve(reflect.TypeFor[deleteview.Input](), reflect.TypeFor[deleteview.Output]())
	require.NoError(t, err)
	resources := resource.New()
	require.NoError(t, resources.Register(deleteview.WriterDatlyResourceNamespace, deleteview.WriterDatlyResources))
	handler, err := writer.New(component, reflect.TypeFor[deleteview.Input](), reflect.TypeFor[deleteview.Output](), "patch")
	require.NoError(t, err)
	artifact, err := bootstrap.BuildArtifact(bootstrap.ArtifactInput{Component: component, InputType: reflect.TypeFor[deleteview.Input](), OutputType: reflect.TypeFor[deleteview.Output](), Handler: handler, HandlerOwnedOutput: true, Resources: resources})
	require.NoError(t, err)
	views, err := viewprovider.New(viewprovider.Config{Dependencies: artifact.ViewDependencies, Input: artifact.Input, SQL: &dsql.SQLComponent{DB: db, Tx: tx}})
	require.NoError(t, err)
	runtime, err := druntime.NewRuntime([]*registry.RegisteredComponent{{Component: artifact.Component, Input: artifact.Input, Output: artifact.Output, OutputType: reflect.TypeFor[deleteview.Output](), Handler: handler, Providers: []locator.Provider{views}, DataSource: dml.Source{DB: db, Tx: tx}}}, druntime.WithResources(resources))
	require.NoError(t, err)
	return runtime
}

func TestRunKeyDeleteSQLiteBatchesOnlyExistingExecutionIDs(t *testing.T) {
	s, _, db := orphanFixture(t)
	ids := []string{"missing", "protocol-preserved"}
	_, err := db.Exec("INSERT INTO run(id,run_kind,status) VALUES('protocol-preserved','agui','pending')")
	require.NoError(t, err)
	for i := 0; i < 130; i++ {
		id := fmt.Sprintf("batch-native-%03d", i)
		_, err = db.Exec("INSERT INTO run(id,status) VALUES(?,'completed')", id)
		require.NoError(t, err)
		ids = append(ids, id)
	}
	ids = append(ids, ids[len(ids)-1])
	require.NoError(t, (&runstore.Store{Invoker: s.Invoker}).DeleteTrusted(context.Background(), ids...))
	var count int
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM run WHERE id LIKE 'batch-native-%'").Scan(&count))
	require.Zero(t, count)
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM run WHERE id='protocol-preserved'").Scan(&count))
	require.Equal(t, 1, count)
}
