package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/executor/config"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	convmem "github.com/viant/agently-core/app/store/data/memory"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
)

func TestEnsureRunTrackedLLMContext_DoesNotFabricateParentMessageID(t *testing.T) {
	t.Parallel()

	recorder := &recordingConvClient{}
	svc := &Service{conversation: recorder}

	ctx := svc.ensureRunTrackedLLMContext(context.Background(), "conv-1", "tool_router", "turn-1")
	turn, ok := runtimerequestctx.TurnMetaFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, "conv-1", turn.ConversationID)
	require.Equal(t, "turn-1", turn.TurnID)
	require.Equal(t, "", turn.ParentMessageID)

	require.NotNil(t, recorder.lastTurn)
	require.Equal(t, "turn-1", recorder.lastTurn.Id)
	require.Nil(t, recorder.lastTurn.StartedByMessageID)
	require.True(t, recorder.lastTurn.Has.AgentIDUsed)
	require.Equal(t, "tool_router", *recorder.lastTurn.AgentIDUsed, "a newly owned helper turn keeps its diagnostic identity")
}

func TestEnsureRunTrackedLLMContext_DoesNotRestateRunningStatusForExistingTurn(t *testing.T) {
	t.Parallel()

	recorder := &recordingConvClient{}
	svc := &Service{conversation: recorder}
	base := runtimerequestctx.WithTurnMeta(context.Background(), runtimerequestctx.TurnMeta{
		ConversationID: "conv-1",
		TurnID:         "turn-1",
		Assistant:      "analyst",
	})

	ctx := svc.ensureRunTrackedLLMContext(base, "conv-1", "intake_sidecar", "turn-1")
	turn, ok := runtimerequestctx.TurnMetaFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, "conv-1", turn.ConversationID)
	require.Equal(t, "turn-1", turn.TurnID)

	require.NotNil(t, recorder.lastTurn)
	require.Equal(t, "turn-1", recorder.lastTurn.Id)
	require.Equal(t, "conv-1", recorder.lastTurn.ConversationID)
	require.NotNil(t, recorder.lastTurn.Has)
	require.False(t, recorder.lastTurn.Has.Status, "helper patch must not restate running lifecycle for an existing turn")
	require.False(t, recorder.lastTurn.Has.AgentIDUsed, "helper must not overwrite the authoritative turn agent")
}

var _ apiconv.Client = (*recordingConvClient)(nil)

func TestIntakeSidecarPreservesNextTurnRoutingIdentity(t *testing.T) {
	ctx := context.Background()
	store := convmem.New()
	conv := apiconv.NewConversation()
	conv.SetId("routing-continuity")
	require.NoError(t, store.PatchConversations(ctx, conv))
	svc := &Service{conversation: store, defaults: &config.Defaults{Agent: "workspace-default"}}
	for _, item := range []struct{ id, agent string }{{"older", "older-agent"}, {"current", "main-agent"}} {
		turn := apiconv.NewTurn()
		turn.SetId(item.id)
		turn.SetConversationID(conv.Id)
		turn.SetAgentIDUsed(item.agent)
		require.NoError(t, store.PatchTurn(ctx, turn))
	}
	main := runtimerequestctx.WithTurnMeta(ctx, runtimerequestctx.TurnMeta{ConversationID: conv.Id, TurnID: "current", Assistant: "main-agent"})
	sidecar := svc.intakeTrackedContext(main, &QueryInput{ConversationID: conv.Id})
	require.Equal(t, "router", runtimerequestctx.RequestModeFromContext(sidecar))
	require.Empty(t, runtimerequestctx.RequestModeFromContext(main), "sidecar mode must remain local")
	fresh, err := store.GetConversation(ctx, conv.Id)
	require.NoError(t, err)
	decision, err := svc.resolveTurnRouting(ctx, fresh, "", "continue this report", "next")
	require.NoError(t, err)
	require.Equal(t, "main-agent", decision.AgentID, "helper attribution must not send follow-up to older agent/default")
	require.Equal(t, "continuity", decision.RoutingReason)
}
