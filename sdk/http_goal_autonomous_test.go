package sdk

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"
	"unsafe"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/store/data"
	"github.com/viant/agently-core/app/store/native"
	"github.com/viant/agently-core/internal/testutil/dbtest"
	agentsvc "github.com/viant/agently-core/service/agent"
	goalsys "github.com/viant/agently-core/service/goal"
	"github.com/viant/agently-core/service/scheduler"
	"github.com/viant/agently-core/workspace"
	"github.com/viant/datly/bootstrap/connector"
	_ "modernc.org/sqlite"
)

func TestHTTPGoalAPI_ExposesAndClearsControllerScheduleAcrossSchedulerResume(t *testing.T) {
	prevRoot := workspace.Root()
	tempRoot := t.TempDir()
	workspace.SetRoot(tempRoot)
	defer workspace.SetRoot(prevRoot)

	err := os.WriteFile(filepath.Join(tempRoot, "config.yaml"), []byte(`
features:
  goals:
    enabled: true
  wakeups:
    enabled: true
    minWakeDelaySeconds: 1
    maxWakeDelaySeconds: 3600
`), 0o644)
	require.NoError(t, err)

	ctx := context.Background()
	server, repo := linkedGoalRepoForSDK(t, "conv-goal")
	convID, objective, status := "conv-goal", "finish parser cleanup", "active"
	require.NoError(t, repo.Apply(ctx, goalsys.Mutation{
		ID:             "goal-conv-goal",
		ConversationID: goalsys.Field[*string]{Present: true, Value: &convID},
		Objective:      goalsys.Field[*string]{Present: true, Value: &objective},
		Status:         goalsys.Field[*string]{Present: true, Value: &status},
	}))
	schedulerSvc, db := newHTTPGoalAutonomousScheduler(t)
	defer db.Close()

	queryCh := make(chan agentsvc.QueryInput, 1)
	setSchedulerQueryRunnerForGoalAPITest(t, schedulerSvc, func(_ context.Context, input *agentsvc.QueryInput, output *agentsvc.QueryOutput) error {
		cp := *input
		if input.Context != nil {
			cp.Context = map[string]any{}
			for k, v := range input.Context {
				cp.Context[k] = v
			}
		}
		queryCh <- cp
		output.ConversationID = input.ConversationID
		output.Content = "wakeup resumed"
		return nil
	})

	backend := &backendClient{goalRepo: repo, goalInvoker: server}
	backend.SetScheduler(schedulerSvc)

	client := newHandlerBackedHTTP(t, NewHandler(backend))

	wakeAt := time.Now().UTC().Add(2 * time.Minute).Truncate(time.Second)
	scheduled, err := schedulerSvc.ScheduleGoalWakeup(ctx, agentsvc.GoalWakeupRequest{
		ConversationID: "conv-goal",
		GoalID:         "goal-conv-goal",
		UserID:         "devuser",
		AgentID:        "coder",
		WakeAt:         wakeAt,
		Preview:        "Continue goal later",
		Payload:        "Continue working toward the active goal.",
	})
	require.NoError(t, err)
	require.True(t, scheduled)

	before, err := client.GetGoal(ctx, "conv-goal")
	require.NoError(t, err)
	require.NotNil(t, before)
	require.NotNil(t, before.ControllerSchedule)
	require.Equal(t, "wakeup", before.ControllerSchedule.Mode)
	require.Equal(t, "Continue goal later", before.ControllerSchedule.Preview)
	require.Equal(t, wakeAt.Format(time.RFC3339Nano), before.ControllerSchedule.WakeAt)

	_, err = db.ExecContext(ctx, `UPDATE schedule SET next_run_at = ? WHERE id = ?`, time.Now().UTC().Add(-1*time.Minute), "goal-wakeup-goal-conv-goal")
	require.NoError(t, err)

	started, err := schedulerSvc.RunDue(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, started)

	var captured agentsvc.QueryInput
	select {
	case captured = <-queryCh:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for scheduler wakeup query")
	}

	require.Equal(t, "conv-goal", captured.ConversationID)
	require.Equal(t, "goal-wakeup-goal-conv-goal", captured.ScheduleId)
	require.Equal(t, "Continue goal later", captured.DisplayQuery)

	require.Eventually(t, func() bool {
		after, err := client.GetGoal(ctx, "conv-goal")
		if err != nil || after == nil {
			return false
		}
		return after.ControllerSchedule == nil && after.Status == "active" && after.Objective == "finish parser cleanup"
	}, 3*time.Second, 20*time.Millisecond)
}

func newHTTPGoalAutonomousScheduler(t *testing.T) (*scheduler.Service, *sql.DB) {
	t.Helper()
	db, dbPath, cleanup := dbtest.CreateTempSQLiteDB(t, "agently-core-sdk-goal-autonomous")
	t.Cleanup(cleanup)
	dbtest.LoadSQLiteSchema(t, db)

	ctx := context.Background()
	_, file, _, _ := runtime.Caller(0)
	sourceRoot := filepath.Join(filepath.Dir(file), "..")
	server, err := native.New(ctx, native.Options{SourceRoot: sourceRoot, Connectors: []connector.Config{
		{Name: "agently", Driver: "sqlite3", DSN: dbPath},
	}})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Shutdown(context.Background())) })

	store, err := scheduler.NewDatlyStore(ctx, server, data.NewService(server))
	require.NoError(t, err)

	return scheduler.New(store, &agentsvc.Service{}, scheduler.WithMaxConcurrentRuns(1)), db
}

func setSchedulerQueryRunnerForGoalAPITest(t *testing.T, svc *scheduler.Service, fn func(context.Context, *agentsvc.QueryInput, *agentsvc.QueryOutput) error) {
	t.Helper()
	rv := reflect.ValueOf(svc).Elem().FieldByName("queryRunner")
	require.True(t, rv.IsValid())
	reflect.NewAt(rv.Type(), unsafe.Pointer(rv.UnsafeAddr())).Elem().Set(reflect.ValueOf(fn))
}
