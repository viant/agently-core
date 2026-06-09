package sdk

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/store/data"
	convw "github.com/viant/agently-core/pkg/agently/conversation/write"
	aggoalwrite "github.com/viant/agently-core/pkg/agently/goal/write"
	agrunwrite "github.com/viant/agently-core/pkg/agently/run/write"
	schrun "github.com/viant/agently-core/pkg/agently/scheduler/run"
	schedulepkg "github.com/viant/agently-core/pkg/agently/scheduler/schedule"
	schedwrite "github.com/viant/agently-core/pkg/agently/scheduler/schedule/write"
	"github.com/viant/agently-core/service/scheduler"
	"github.com/viant/agently-core/workspace"
)

func TestBackendClient_CreateGoalRejectedWhenWorkspaceDisablesGoals(t *testing.T) {
	prevRoot := workspace.Root()
	tempRoot := t.TempDir()
	workspace.SetRoot(tempRoot)
	defer workspace.SetRoot(prevRoot)

	err := os.WriteFile(filepath.Join(tempRoot, "config.yaml"), []byte(`
features:
  goals:
    enabled: false
`), 0o644)
	require.NoError(t, err)

	ctx := context.Background()
	dataSvc, err := data.NewThinServiceInMemory(ctx)
	require.NoError(t, err)
	_, err = dataSvc.PatchConversations(ctx, []*convw.Conversation{
		convw.NewMutableConversationView(convw.WithConversationID("conv-goal")),
	})
	require.NoError(t, err)

	client := &backendClient{data: dataSvc}
	_, err = client.CreateGoal(ctx, &CreateGoalInput{
		ConversationID: "conv-goal",
		Objective:      "finish parser cleanup",
	})
	require.Error(t, err)
	require.True(t, isFeatureDisabledError(err))
}

func TestStatusForGoalError_UsesForbiddenForFeatureDisabled(t *testing.T) {
	got := statusForGoalError(newFeatureDisabledError("goals are not enabled in this workspace"))
	require.Equal(t, 403, got)
}

func TestBackendClient_GetGoalIncludesPendingControllerSchedule(t *testing.T) {
	prevRoot := workspace.Root()
	tempRoot := t.TempDir()
	workspace.SetRoot(tempRoot)
	defer workspace.SetRoot(prevRoot)

	err := os.WriteFile(filepath.Join(tempRoot, "config.yaml"), []byte(`
features:
  goals:
    enabled: true
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

	wakeAt := time.Now().UTC().Add(15 * time.Minute).Truncate(time.Second)
	schedulerSvc := scheduler.New(&goalWakeupStoreStub{
		dueRows: []*schedulepkg.ScheduleView{
			{
				Id:             "goal-wakeup-goal-conv-goal",
				Internal:       true,
				Enabled:        true,
				ConversationId: stringPtrForGoalTest("conv-goal"),
				GoalId:         stringPtrForGoalTest("goal-conv-goal"),
				Description:    stringPtrForGoalTest("Resume after index rebuild"),
				NextRunAt:      timePtrForGoalTest(wakeAt),
			},
		},
	}, nil)

	client := &backendClient{data: dataSvc}
	client.SetScheduler(schedulerSvc)

	goal, err := client.GetGoal(ctx, "conv-goal")
	require.NoError(t, err)
	require.NotNil(t, goal)
	require.NotNil(t, goal.ControllerSchedule)
	require.Equal(t, "wakeup", goal.ControllerSchedule.Mode)
	require.Equal(t, "Resume after index rebuild", goal.ControllerSchedule.Preview)
	require.Equal(t, wakeAt.Format(time.RFC3339Nano), goal.ControllerSchedule.WakeAt)
}

type goalWakeupStoreStub struct {
	dueRows []*schedulepkg.ScheduleView
}

func (s *goalWakeupStoreStub) Get(context.Context, string) (*schedulepkg.ScheduleView, error) {
	return nil, nil
}

func (s *goalWakeupStoreStub) List(context.Context) ([]*schedulepkg.ScheduleView, error) {
	return nil, nil
}

func (s *goalWakeupStoreStub) ListRuns(context.Context, *schrun.RunListInput, int, int) (*scheduler.RunListPage, error) {
	return nil, nil
}

func (s *goalWakeupStoreStub) ListForRunDue(context.Context) ([]*schedulepkg.ScheduleView, error) {
	return s.dueRows, nil
}

func (s *goalWakeupStoreStub) DeleteSchedule(context.Context, string) error {
	return nil
}

func (s *goalWakeupStoreStub) PatchSchedule(context.Context, *schedwrite.Schedule) error {
	return nil
}

func (s *goalWakeupStoreStub) PatchRuns(context.Context, []*agrunwrite.MutableRunView) error {
	return nil
}

func (s *goalWakeupStoreStub) ListRunsForDue(context.Context, string, *time.Time, []string) ([]*schrun.RunView, error) {
	return nil, nil
}

func (s *goalWakeupStoreStub) TryClaimSchedule(context.Context, string, string, time.Time) (bool, error) {
	return false, nil
}

func (s *goalWakeupStoreStub) ReleaseScheduleLease(context.Context, string, string) (bool, error) {
	return false, nil
}

func (s *goalWakeupStoreStub) TryClaimRun(context.Context, string, string, time.Time) (bool, error) {
	return false, nil
}

func (s *goalWakeupStoreStub) ReleaseRunLease(context.Context, string, string) (bool, error) {
	return false, nil
}

func stringPtrForGoalTest(value string) *string {
	return &value
}

func timePtrForGoalTest(value time.Time) *time.Time {
	return &value
}
