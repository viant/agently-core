package sdk

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	authctx "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/protocol/agui/extensions"
	"github.com/viant/agently-core/workspace"
)

type aguiGoalClient struct {
	Client
	calls   int
	goal    *Goal
	created *CreateGoalInput
	updated *UpdateGoalInput
}

func (c *aguiGoalClient) GetGoal(context.Context, string) (*Goal, error) {
	c.calls++
	return c.goal, nil
}
func (c *aguiGoalClient) CreateGoal(_ context.Context, input *CreateGoalInput) (*Goal, error) {
	c.calls++
	c.created = input
	c.goal = &Goal{ID: "g", ConversationID: input.ConversationID, Objective: input.Objective, Status: "active", TokenBudget: input.TokenBudget, ControllerSpec: input.ControllerSpec}
	return c.goal, nil
}
func (c *aguiGoalClient) UpdateGoal(_ context.Context, input *UpdateGoalInput) (*Goal, error) {
	c.calls++
	c.updated = input
	return c.goal, nil
}
func (c *aguiGoalClient) ClearGoal(context.Context, string) error {
	c.calls++
	c.goal = nil
	return nil
}
func (c *aguiGoalClient) PauseGoal(_ context.Context, _ string, reason string) (*Goal, error) {
	c.calls++
	c.goal.Status = "paused"
	c.goal.PauseReason = reason
	return c.goal, nil
}
func (c *aguiGoalClient) ResumeGoal(context.Context, string) (*Goal, error) {
	c.calls++
	c.goal.Status = "active"
	c.goal.PauseReason = ""
	return c.goal, nil
}

func TestAGUIGoalCommandsStrictPayloadAndScope(t *testing.T) {
	ctx := context.Background()
	client := &aguiGoalClient{}
	for _, test := range []struct{ op, payload string }{
		{"goal.create", `{"objective":"work","userId":"intruder"}`},
		{"goal.create", `{"objective":"work","conversationId":"other"}`},
		{"goal.create", `{"objective":"work","tokenBudget":null}`},
		{"goal.create", `{"objective":"work","tokenBudget":-1}`},
		{"goal.create", `{"objective":"work","controllerSpec":{"continueMode":"idle_only","onTurnFinished":"evaluate","onAsyncCompleted":"evaluate","unknown":true}}`},
		{"goal.create", `{"objective":"work","controllerSpec":"{\"continueMode\":\"idle_only\",\"onTurnFinished\":\"evaluate\",\"onAsyncCompleted\":\"evaluate\",\"unknown\":true}"}`},
		{"goal.update", `{}`}, {"goal.update", `{"status":"unknown"}`}, {"goal.update", `{"statusReason":" "}`},
		{"goal.clear", `{"reason":"unexpected"}`}, {"goal.pause", `{"reason":null}`}, {"goal.resume", `{"tokenBudget":100}`},
		{"goal.get", `null`}, {"goal.get", `{} {}`},
	} {
		_, handled, err := dispatchAGUIGoal(ctx, client, "thread", test.op, json.RawMessage(test.payload))
		require.True(t, handled)
		require.Error(t, err, "%s %s", test.op, test.payload)
	}
	require.Zero(t, client.calls)
	result, handled, err := dispatchAGUIGoal(ctx, client, "thread", "goal.create", json.RawMessage(`{"objective":"work","tokenBudget":0,"controllerSpec":{"continueMode":"idle_only","onTurnFinished":"evaluate","onAsyncCompleted":"wait"}}`))
	require.NoError(t, err)
	require.True(t, handled)
	require.Equal(t, "thread", client.created.ConversationID)
	require.NotNil(t, client.created.TokenBudget)
	require.Zero(t, *client.created.TokenBudget)
	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	require.NoError(t, extensions.ValidateGoalResult(encoded))
	require.Contains(t, string(encoded), `"tokensUsed":0`)
	require.Contains(t, string(encoded), `"controllerSchedule":null`)
	_, _, err = dispatchAGUIGoal(ctx, client, "thread", "goal.pause", nil)
	require.NoError(t, err)
	require.Equal(t, "user_requested", client.goal.PauseReason)
	_, _, err = dispatchAGUIGoal(ctx, client, "thread", "goal.resume", nil)
	require.NoError(t, err)
	require.Equal(t, "active", client.goal.Status)
	_, handled, err = dispatchAGUIGoal(ctx, client, "thread", "chat", nil)
	require.NoError(t, err)
	require.False(t, handled)
}

func TestAGUIGoalCommandsUseDatlyWithoutModelExecution(t *testing.T) {
	previous := workspace.Root()
	root := t.TempDir()
	workspace.SetRoot(root)
	defer workspace.SetRoot(previous)
	require.NoError(t, os.WriteFile(filepath.Join(root, "config.yaml"), []byte("features:\n  goals:\n    enabled: true\n"), 0600))
	ctx := context.Background()
	server, repository := linkedGoalRepoForSDK(t, "goal-thread")
	client := &backendClient{goalRepo: repository, goalInvoker: server}
	// This backend has no agent execution service: these commands cannot rely on Query.
	execute := func(op, payload string) *AGUIGoalCommandResult {
		result, handled, err := dispatchAGUIGoal(ctx, client, "goal-thread", op, json.RawMessage(payload))
		require.NoError(t, err)
		require.True(t, handled)
		return result.(*AGUIGoalCommandResult)
	}
	require.Nil(t, execute("goal.get", `{}`).Goal)
	created := execute("goal.create", `{"objective":"finish deterministic commands","tokenBudget":1000,"controllerSpec":{"continueMode":"idle_only","onTurnFinished":"evaluate","onAsyncCompleted":"wait"}}`)
	require.Equal(t, "active", created.Goal.Status)
	require.Equal(t, int64(1000), *created.Goal.TokenBudget)
	updated := execute("goal.update", `{"objective":"finish verified commands","tokenBudget":2000}`)
	require.Equal(t, "finish verified commands", updated.Goal.Objective)
	paused := execute("goal.pause", `{}`)
	require.Equal(t, "paused", paused.Goal.Status)
	require.Equal(t, "user_requested", paused.Goal.PauseReason)
	resumed := execute("goal.resume", `{}`)
	require.Equal(t, "active", resumed.Goal.Status)
	require.Empty(t, resumed.Goal.PauseReason)
	require.Empty(t, resumed.Goal.StatusReason)
	complete := execute("goal.update", `{"status":"complete","statusReason":"verified"}`)
	require.Equal(t, "complete", complete.Goal.Status)
	require.Equal(t, "verified", complete.Goal.StatusReason)
	require.True(t, execute("goal.clear", `{}`).Cleared)
	require.Nil(t, execute("goal.get", `{}`).Goal)
}

func TestAGUIGoalLifecycleCannotOverrideConversationAuthorization(t *testing.T) {
	previous := workspace.Root()
	root := t.TempDir()
	workspace.SetRoot(root)
	defer workspace.SetRoot(previous)
	require.NoError(t, os.WriteFile(filepath.Join(root, "config.yaml"), []byte("features:\n  goals:\n    enabled: true\n"), 0600))
	server, repository := linkedGoalRepoForSDK(t, "private-goal", "owner")
	client := &backendClient{goalRepo: repository, goalInvoker: server}
	owner := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "owner"})
	_, err := client.CreateGoal(owner, &CreateGoalInput{ConversationID: "private-goal", Objective: "private work"})
	require.NoError(t, err)
	intruder := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "intruder"})
	_, _, err = dispatchAGUIGoal(intruder, client, "private-goal", "goal.pause", nil)
	require.Error(t, err)
	_, _, err = dispatchAGUIGoal(intruder, client, "private-goal", "goal.resume", nil)
	require.Error(t, err)
	unchanged, err := client.GetGoal(owner, "private-goal")
	require.NoError(t, err)
	require.Equal(t, "active", unchanged.Status)
}
