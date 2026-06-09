package sdk

import (
	"context"
	"database/sql"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
	"unsafe"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/store/data"
	"github.com/viant/agently-core/internal/testutil/dbtest"
	convw "github.com/viant/agently-core/pkg/agently/conversation/write"
	aggoalwrite "github.com/viant/agently-core/pkg/agently/goal/write"
	agrunwrite "github.com/viant/agently-core/pkg/agently/run/write"
	agentsvc "github.com/viant/agently-core/service/agent"
	"github.com/viant/agently-core/service/scheduler"
	"github.com/viant/agently-core/workspace"
	"github.com/viant/datly"
	"github.com/viant/datly/view"
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
	dataSvc, err := data.NewThinServiceInMemory(ctx)
	require.NoError(t, err)
	_, err = dataSvc.PatchConversations(ctx, []*convw.Conversation{
		convw.NewMutableConversationView(convw.WithConversationID("conv-goal")),
	})
	require.NoError(t, err)
	_, err = dataSvc.PatchGoals(ctx, []*aggoalwrite.MutableGoalView{
		aggoalwrite.NewMutableGoalView(
			aggoalwrite.WithGoalID("goal-conv-goal"),
			aggoalwrite.WithGoalConversationID("conv-goal"),
			aggoalwrite.WithGoalObjective("finish parser cleanup"),
			aggoalwrite.WithGoalStatus("active"),
		),
	})
	require.NoError(t, err)

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

	backend := &backendClient{data: dataSvc}
	backend.SetScheduler(schedulerSvc)

	server := httptest.NewServer(NewHandler(backend))
	defer server.Close()

	client, err := NewHTTP(server.URL, WithHTTPClient(server.Client()))
	require.NoError(t, err)

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
	dao, err := datly.New(ctx)
	require.NoError(t, err)
	require.NoError(t, dao.AddConnectors(ctx, view.NewConnector("agently", "sqlite", dbPath)))

	store, err := scheduler.NewDatlyStore(ctx, dao, nil)
	require.NoError(t, err)
	_, err = agrunwrite.DefineComponent(ctx, dao)
	require.NoError(t, err)

	return scheduler.New(store, &agentsvc.Service{}, scheduler.WithMaxConcurrentRuns(1)), db
}

func setSchedulerQueryRunnerForGoalAPITest(t *testing.T, svc *scheduler.Service, fn func(context.Context, *agentsvc.QueryInput, *agentsvc.QueryOutput) error) {
	t.Helper()
	rv := reflect.ValueOf(svc).Elem().FieldByName("queryRunner")
	require.True(t, rv.IsValid())
	reflect.NewAt(rv.Type(), unsafe.Pointer(rv.UnsafeAddr())).Elem().Set(reflect.ValueOf(fn))
}
