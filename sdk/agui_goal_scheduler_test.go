package sdk

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	agentsvc "github.com/viant/agently-core/service/agent"
	"github.com/viant/agently-core/workspace"
)

func TestAGUIGoalPauseResumeUsesSchedulerHooks(t *testing.T) {
	previous := workspace.Root()
	root := t.TempDir()
	workspace.SetRoot(root)
	defer workspace.SetRoot(previous)
	require.NoError(t, os.WriteFile(filepath.Join(root, "config.yaml"), []byte("features:\n  goals:\n    enabled: true\n  wakeups:\n    enabled: true\n    minWakeDelaySeconds: 1\n    maxWakeDelaySeconds: 3600\n"), 0600))
	ctx := context.Background()
	server, repo := linkedGoalRepoForSDK(t, "goal-scheduled")
	client := &backendClient{goalRepo: repo, goalInvoker: server}
	goal, err := client.CreateGoal(ctx, &CreateGoalInput{ConversationID: "goal-scheduled", Objective: "scheduled work"})
	require.NoError(t, err)
	schedulerSvc, db := newHTTPGoalAutonomousScheduler(t)
	defer db.Close()
	client.SetScheduler(schedulerSvc)
	scheduled, err := schedulerSvc.ScheduleGoalWakeup(ctx, agentsvc.GoalWakeupRequest{ConversationID: "goal-scheduled", GoalID: goal.ID, UserID: "devuser", AgentID: "coder", WakeAt: time.Now().UTC().Add(time.Hour), Preview: "Continue later", Payload: "Continue the active goal."})
	require.NoError(t, err)
	require.True(t, scheduled)
	result, _, err := dispatchAGUIGoal(ctx, client, "goal-scheduled", "goal.get", nil)
	require.NoError(t, err)
	snapshot := result.(*AGUIGoalCommandResult).Goal
	require.NotNil(t, snapshot.ControllerSchedule)
	require.Equal(t, "wakeup", snapshot.ControllerSchedule.Mode)
	require.Equal(t, "Continue later", snapshot.ControllerSchedule.Preview)
	result, _, err = dispatchAGUIGoal(ctx, client, "goal-scheduled", "goal.pause", nil)
	require.NoError(t, err)
	snapshot = result.(*AGUIGoalCommandResult).Goal
	require.Equal(t, "paused", snapshot.Status)
	require.Nil(t, snapshot.ControllerSchedule)
	require.Nil(t, schedulerSvc.CurrentGoalWakeup(ctx, "goal-scheduled", goal.ID))
	result, _, err = dispatchAGUIGoal(ctx, client, "goal-scheduled", "goal.resume", json.RawMessage(`{}`))
	require.NoError(t, err)
	snapshot = result.(*AGUIGoalCommandResult).Goal
	require.Equal(t, "active", snapshot.Status)
	require.Empty(t, snapshot.PauseReason)
	require.Nil(t, snapshot.ControllerSchedule)
}
