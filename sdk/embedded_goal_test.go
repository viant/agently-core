package sdk

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/store/native"
	authctx "github.com/viant/agently-core/internal/auth"
	runmodel "github.com/viant/agently-core/model/run"
	schedulemodel "github.com/viant/agently-core/model/schedule"
	scheduledrunmodel "github.com/viant/agently-core/model/scheduledrun"

	goalsys "github.com/viant/agently-core/service/goal"
	"github.com/viant/agently-core/service/scheduler"
	"github.com/viant/agently-core/workspace"
	"github.com/viant/datly/standalone"
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
	client := &backendClient{}
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
	server, repo := linkedGoalRepoForSDK(t, "conv-goal")
	convID, objective, status := "conv-goal", "finish parser cleanup", "active"
	require.NoError(t, repo.Apply(ctx, goalsys.Mutation{
		ID:             "goal-conv-goal",
		ConversationID: goalsys.Field[*string]{Present: true, Value: &convID},
		Objective:      goalsys.Field[*string]{Present: true, Value: &objective},
		Status:         goalsys.Field[*string]{Present: true, Value: &status},
	}))
	wakeAt := time.Now().UTC().Add(15 * time.Minute).Truncate(time.Second)
	schedulerSvc := scheduler.New(&goalWakeupStoreStub{
		dueRows: []*schedulemodel.ScheduleView{
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

	client := &backendClient{goalRepo: repo, goalInvoker: server}
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
	dueRows []*schedulemodel.ScheduleView
}

func (s *goalWakeupStoreStub) Get(context.Context, string) (*schedulemodel.ScheduleView, error) {
	return nil, nil
}

func (s *goalWakeupStoreStub) List(context.Context) ([]*schedulemodel.ScheduleView, error) {
	return nil, nil
}

func (s *goalWakeupStoreStub) ListRuns(context.Context, *scheduledrunmodel.RunListInput, int, int) (*scheduler.RunListPage, error) {
	return nil, nil
}

func (s *goalWakeupStoreStub) ListForRunDue(context.Context) ([]*schedulemodel.ScheduleView, error) {
	return s.dueRows, nil
}

func (s *goalWakeupStoreStub) DeleteSchedule(context.Context, string) error {
	return nil
}

func (s *goalWakeupStoreStub) DeleteScheduledRun(context.Context, string) error {
	return nil
}

func (s *goalWakeupStoreStub) PatchSchedule(context.Context, *schedulemodel.Schedule) error {
	return nil
}

func (s *goalWakeupStoreStub) PatchRuns(context.Context, []*runmodel.MutableRunView) error {
	return nil
}

func (s *goalWakeupStoreStub) ListRunsForDue(context.Context, string, *time.Time, []string) ([]*scheduledrunmodel.RunView, error) {
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

func linkedGoalRepoForSDK(t *testing.T, conversationID string, privateOwner ...string) (*standalone.Server, goalsys.Repository) {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Join(filepath.Dir(file), "..")
	workspaceRoot := t.TempDir()
	ctx := context.Background()
	server, err := native.New(ctx, native.Options{SourceRoot: project, WorkspaceRoot: workspaceRoot})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Shutdown(context.Background())) })
	db, err := sql.Open("sqlite3", filepath.Join(workspaceRoot, "db", "agently-core.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	_, err = db.ExecContext(ctx, "INSERT INTO conversation(id,visibility) VALUES (?,'public')", conversationID)
	require.NoError(t, err)
	if len(privateOwner) > 0 {
		_, err = db.ExecContext(ctx, "UPDATE conversation SET visibility='private', created_by_user_id=? WHERE id=?", privateOwner[0], conversationID)
		require.NoError(t, err)
	}
	return server, goalsys.NewStore(server)
}

func TestBackendClient_PrivateGoalRequiresConversationOwner(t *testing.T) {
	prevRoot := workspace.Root()
	tempRoot := t.TempDir()
	workspace.SetRoot(tempRoot)
	defer workspace.SetRoot(prevRoot)
	require.NoError(t, os.WriteFile(filepath.Join(tempRoot, "config.yaml"), []byte("features:\n  goals:\n    enabled: true\n"), 0o644))
	ctx := context.Background()
	server, repo := linkedGoalRepoForSDK(t, "private-c1", "u1")
	conversationID, objective, status := "private-c1", "private objective", "active"
	require.NoError(t, repo.Apply(ctx, goalsys.Mutation{
		ID: "g-private", ConversationID: goalsys.Field[*string]{Present: true, Value: &conversationID},
		Objective: goalsys.Field[*string]{Present: true, Value: &objective},
		Status:    goalsys.Field[*string]{Present: true, Value: &status},
	}))
	client := &backendClient{goalRepo: repo, goalInvoker: server}
	other := authctx.WithUserInfo(ctx, &authctx.UserInfo{Subject: "u2"})
	_, err := client.GetGoal(other, conversationID)
	require.ErrorIs(t, err, native.ErrConversationNotVisible)
	_, err = client.UpdateGoal(other, &UpdateGoalInput{ConversationID: conversationID, Status: "blocked"})
	require.ErrorIs(t, err, native.ErrConversationNotVisible)
	require.ErrorIs(t, client.ClearGoal(other, conversationID), native.ErrConversationNotVisible)
	owner := authctx.WithUserInfo(ctx, &authctx.UserInfo{Subject: "u1"})
	goal, err := client.GetGoal(owner, conversationID)
	require.NoError(t, err)
	require.Equal(t, "private objective", goal.Objective)
}
