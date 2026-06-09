package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	convcli "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/app/store/data"
	convmem "github.com/viant/agently-core/app/store/data/memory"
	convw "github.com/viant/agently-core/pkg/agently/conversation/write"
	aggoalwrite "github.com/viant/agently-core/pkg/agently/goal/write"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/runtime/streaming"
	goalsys "github.com/viant/agently-core/service/goal"
	"github.com/viant/agently-core/workspace"
)

type captureGoalWakeupScheduler struct {
	requests []GoalWakeupRequest
	cancels  []struct {
		conversationID string
		goalID         string
	}
}

func (c *captureGoalWakeupScheduler) ScheduleGoalWakeup(_ context.Context, req GoalWakeupRequest) (bool, error) {
	c.requests = append(c.requests, req)
	return true, nil
}

func (c *captureGoalWakeupScheduler) CancelGoalWakeups(_ context.Context, conversationID, goalID string) error {
	c.cancels = append(c.cancels, struct {
		conversationID string
		goalID         string
	}{conversationID: conversationID, goalID: goalID})
	return nil
}

func TestMaybeContinueActiveGoal_SchedulesWakeupWhenControllerRequestsDelay(t *testing.T) {
	ctx := context.Background()
	dataSvc, err := data.NewThinServiceInMemory(ctx)
	require.NoError(t, err)
	convRow := convw.NewMutableConversationView(convw.WithConversationID("conv-wakeup"))
	convRow.SetAgentId("coder")
	_, err = dataSvc.PatchConversations(ctx, []*convw.Conversation{convRow})
	require.NoError(t, err)

	spec, err := (&goalsys.ControllerSpec{
		ContinueMode:     goalsys.ContinueModeIdleOnly,
		OnTurnFinished:   goalsys.TurnPolicyEvaluate,
		OnAsyncCompleted: goalsys.AsyncPolicyEvaluate,
		WakeDelaySeconds: intPtr(90),
	}).Encode()
	require.NoError(t, err)
	_, err = dataSvc.PatchGoals(ctx, []*aggoalwrite.MutableGoalView{
		aggoalwrite.NewMutableGoalView(
			aggoalwrite.WithGoalID("goal-conv-wakeup"),
			aggoalwrite.WithGoalConversationID("conv-wakeup"),
			aggoalwrite.WithGoalObjective("finish cleanup"),
			aggoalwrite.WithGoalStatus("active"),
			aggoalwrite.WithGoalControllerSpec(spec),
		),
	})
	require.NoError(t, err)

	wakeups := &captureGoalWakeupScheduler{}
	pub := &captureGoalEventPublisher{}
	svc := &Service{
		dataService:  dataSvc,
		conversation: convmem.New(),
		goalRuntime:  goalsys.NewRuntime(goalsys.NewStore(dataSvc)),
		goalWakeups:  wakeups,
		streamPub:    pub,
	}

	input := &QueryInput{
		RequestTime:    time.Now().Add(-2 * time.Minute),
		ConversationID: "conv-wakeup",
		AgentID:        "coder",
		UserId:         "devuser",
		Query:          "continue",
		Agent:          &agentmdl.Agent{},
	}
	output := &QueryOutput{Content: "continuationHint: Re-open the report after dependencies settle."}
	svc.maybeContinueActiveGoal(ctx, input, output, runtimerequestctx.TurnMeta{ConversationID: "conv-wakeup"}, "succeeded")

	require.Len(t, wakeups.requests, 1)
	require.Equal(t, "conv-wakeup", wakeups.requests[0].ConversationID)
	require.Equal(t, "goal-conv-wakeup", wakeups.requests[0].GoalID)
	require.Equal(t, "coder", wakeups.requests[0].AgentID)
	require.Equal(t, "devuser", wakeups.requests[0].UserID)
	require.Contains(t, wakeups.requests[0].Preview, "Continue:")
	require.Contains(t, wakeups.requests[0].Payload, "Re-open the report")
	require.WithinDuration(t, time.Now().Add(90*time.Second), wakeups.requests[0].WakeAt, 3*time.Second)
	require.True(t, pub.HasEvent(streaming.EventTypeGoalControllerScheduled))
	pub.mu.Lock()
	defer pub.mu.Unlock()
	require.Len(t, pub.events, 1)
	require.Equal(t, "scheduled", pub.events[0].Status)
	require.Equal(t, "wakeup", pub.events[0].Patch["mode"])
	require.NotEmpty(t, pub.events[0].Patch["wakeAt"])
}

func TestMaybeContinueActiveGoal_ClampsWakeDelayToWorkspaceMinimum(t *testing.T) {
	prevRoot := workspace.Root()
	tempRoot := t.TempDir()
	workspace.SetRoot(tempRoot)
	defer workspace.SetRoot(prevRoot)
	require.NoError(t, os.WriteFile(filepath.Join(tempRoot, "config.yaml"), []byte(`
features:
  wakeups:
    enabled: true
    minWakeDelaySeconds: 300
    maxWakeDelaySeconds: 900
`), 0o644))

	ctx := context.Background()
	dataSvc, err := data.NewThinServiceInMemory(ctx)
	require.NoError(t, err)
	convRow := convw.NewMutableConversationView(convw.WithConversationID("conv-wakeup"))
	convRow.SetAgentId("coder")
	_, err = dataSvc.PatchConversations(ctx, []*convw.Conversation{convRow})
	require.NoError(t, err)

	spec, err := (&goalsys.ControllerSpec{
		ContinueMode:     goalsys.ContinueModeIdleOnly,
		OnTurnFinished:   goalsys.TurnPolicyEvaluate,
		OnAsyncCompleted: goalsys.AsyncPolicyEvaluate,
		WakeDelaySeconds: intPtr(90),
	}).Encode()
	require.NoError(t, err)
	_, err = dataSvc.PatchGoals(ctx, []*aggoalwrite.MutableGoalView{
		aggoalwrite.NewMutableGoalView(
			aggoalwrite.WithGoalID("goal-conv-wakeup"),
			aggoalwrite.WithGoalConversationID("conv-wakeup"),
			aggoalwrite.WithGoalObjective("finish cleanup"),
			aggoalwrite.WithGoalStatus("active"),
			aggoalwrite.WithGoalControllerSpec(spec),
		),
	})
	require.NoError(t, err)

	wakeups := &captureGoalWakeupScheduler{}
	svc := &Service{
		dataService:  dataSvc,
		conversation: convmem.New(),
		goalRuntime:  goalsys.NewRuntime(goalsys.NewStore(dataSvc)),
		goalWakeups:  wakeups,
	}

	input := &QueryInput{
		RequestTime:    time.Now().Add(-2 * time.Minute),
		ConversationID: "conv-wakeup",
		AgentID:        "coder",
		UserId:         "devuser",
		Query:          "continue",
		Agent:          &agentmdl.Agent{},
	}
	output := &QueryOutput{Content: "continuationHint: Re-open the report after dependencies settle."}
	svc.maybeContinueActiveGoal(ctx, input, output, runtimerequestctx.TurnMeta{ConversationID: "conv-wakeup"}, "succeeded")

	require.Len(t, wakeups.requests, 1)
	require.WithinDuration(t, time.Now().Add(300*time.Second), wakeups.requests[0].WakeAt, 3*time.Second)
}

func TestMaybeContinueActiveGoal_FallsBackToImmediateQueueWhenWakeupsDisabled(t *testing.T) {
	prevRoot := workspace.Root()
	tempRoot := t.TempDir()
	workspace.SetRoot(tempRoot)
	defer workspace.SetRoot(prevRoot)
	require.NoError(t, os.WriteFile(filepath.Join(tempRoot, "config.yaml"), []byte(`
features:
  wakeups:
    enabled: false
`), 0o644))

	ctx := context.Background()
	dataSvc, err := data.NewThinServiceInMemory(ctx)
	require.NoError(t, err)
	_, err = dataSvc.PatchConversations(ctx, []*convw.Conversation{
		convw.NewMutableConversationView(convw.WithConversationID("conv-goal")),
	})
	require.NoError(t, err)

	spec, err := (&goalsys.ControllerSpec{
		ContinueMode:     goalsys.ContinueModeIdleOnly,
		OnTurnFinished:   goalsys.TurnPolicyEvaluate,
		OnAsyncCompleted: goalsys.AsyncPolicyEvaluate,
		WakeDelaySeconds: intPtr(120),
	}).Encode()
	require.NoError(t, err)
	_, err = dataSvc.PatchGoals(ctx, []*aggoalwrite.MutableGoalView{
		aggoalwrite.NewMutableGoalView(
			aggoalwrite.WithGoalID("goal-conv-goal"),
			aggoalwrite.WithGoalConversationID("conv-goal"),
			aggoalwrite.WithGoalObjective("finish parser cleanup"),
			aggoalwrite.WithGoalStatus("active"),
			aggoalwrite.WithGoalControllerSpec(spec),
		),
	})
	require.NoError(t, err)

	convClient := convmem.New()
	conv := convcli.NewConversation()
	conv.SetId("conv-goal")
	conv.SetCreatedAt(time.Now())
	require.NoError(t, convClient.PatchConversations(ctx, conv))

	wakeups := &captureGoalWakeupScheduler{}
	svc := &Service{
		dataService:  dataSvc,
		conversation: convClient,
		goalRuntime:  goalsys.NewRuntime(goalsys.NewStore(dataSvc)),
		goalWakeups:  wakeups,
	}

	input := &QueryInput{
		RequestTime:    time.Now().Add(-2 * time.Minute),
		ConversationID: "conv-goal",
		AgentID:        "coder",
		UserId:         "devuser",
		Query:          "continue",
		Agent:          &agentmdl.Agent{},
	}
	output := &QueryOutput{Content: "continuationHint: Re-open the report after dependencies settle."}
	svc.maybeContinueActiveGoal(ctx, input, output, runtimerequestctx.TurnMeta{ConversationID: "conv-goal"}, "succeeded")

	require.Len(t, wakeups.requests, 0)
	require.Eventually(t, func() bool {
		got, err := convClient.GetConversation(ctx, "conv-goal")
		if err != nil || got == nil {
			return false
		}
		for _, turn := range got.GetTranscript() {
			if turn == nil {
				continue
			}
			for _, msg := range turn.Message {
				if msg == nil || msg.RawContent == nil {
					continue
				}
				if strings.Contains(strings.TrimSpace(*msg.RawContent), "Re-open the report after dependencies settle.") {
					return true
				}
			}
		}
		return false
	}, 3*time.Second, 20*time.Millisecond)
}
