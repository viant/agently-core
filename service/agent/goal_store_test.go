package agent

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
	write "github.com/viant/agently-core/internal/datly/goal/write"
	goalsys "github.com/viant/agently-core/service/goal"
	"github.com/viant/datly/bootstrap/connector"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/spec"
	"github.com/viant/datly/standalone"
	"github.com/viant/datly/standalone/config"
)

func newLinkedGoalStore(t *testing.T, id, conversationID, objective, controller string) goalsys.Store {
	t.Helper()
	ctx := context.Background()
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Join(filepath.Dir(file), "..", "..")
	dsn := filepath.Join(t.TempDir(), "goals.db") + "?_foreign_keys=on"
	db, err := sql.Open("sqlite3", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	schema, err := os.ReadFile(filepath.Join(project, "tools", "schema", "schema.sql"))
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, string(schema))
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, "INSERT INTO conversation(id) VALUES (?)", conversationID)
	require.NoError(t, err)
	server, err := standalone.New(ctx, standalone.Options{
		Config: &config.Config{
			BaseDir:     project,
			Connector:   "agently",
			Connectors:  []connector.Config{{Name: "agently", Driver: "sqlite3", DSN: dsn, MaxOpenConns: 2}},
			GoBootstrap: &config.Packages{Packages: []string{"github.com/viant/agently-core/internal/datly/goal/..."}},
		},
		RequireLinked: true,
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Shutdown(context.Background())) })
	require.NoError(t, server.Reload(ctx, 1))
	row := &write.Goal{}
	row.SetId(id)
	row.SetConversationId(&conversationID)
	row.SetObjective(&objective)
	status := "active"
	row.SetStatus(&status)
	row.SetControllerSpec(&controller)
	input := &write.Input{}
	input.SetGoals([]*write.Goal{row})
	_, err = server.InvokeComponent(ctx, dexec.ComponentRequest{
		Target: dexec.ComponentTarget{
			Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[write.WriterComponent]().PkgPath(), Name: "writer"},
			Route:     spec.RouteRef{Method: "PATCH", Path: "/v1/api/agently/goal"},
		},
		Input: input,
	})
	require.NoError(t, err)
	return goalsys.NewStore(server)
}

func TestGoalStoreWiring(t *testing.T) {
	ctx := context.Background()
	controller, err := (&goalsys.ControllerSpec{
		ContinueMode:     goalsys.ContinueModeIdleOnly,
		OnTurnFinished:   goalsys.TurnPolicyEvaluate,
		OnAsyncCompleted: goalsys.AsyncPolicyWait,
	}).Encode()
	require.NoError(t, err)
	store := newLinkedGoalStore(t, "g1", "c1", "finish migration", controller)
	svc := &Service{}
	WithGoalStore(store)(svc)
	require.NotNil(t, svc.goalRuntime)
	current, err := svc.activeGoalReader().Current(ctx, "c1")
	require.NoError(t, err)
	require.Equal(t, "g1", current.ID)
	require.Equal(t, goalsys.AsyncPolicyWait, svc.currentGoalAsyncPolicy(ctx, "c1"))
	require.NoError(t, store.Transition(ctx, "g1", goalsys.StatusComplete, "done"))
	current, err = svc.goalRuntime.Current(ctx, "c1")
	require.NoError(t, err)
	require.Equal(t, goalsys.StatusComplete, current.Status)
	WithGoalStore(nil)(svc)
	require.Nil(t, svc.activeGoalReader())
	require.Equal(t, goalsys.AsyncPolicyEvaluate, svc.currentGoalAsyncPolicy(ctx, "c1"))
}
