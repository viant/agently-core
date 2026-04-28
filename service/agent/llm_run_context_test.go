package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	apiconv "github.com/viant/agently-core/app/store/conversation"
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
}

func TestEnsureRunTrackedLLMContext_DoesNotRestateRunningStatusForExistingTurn(t *testing.T) {
	t.Parallel()

	recorder := &recordingConvClient{}
	svc := &Service{conversation: recorder}
	base := runtimerequestctx.WithTurnMeta(context.Background(), runtimerequestctx.TurnMeta{
		ConversationID: "conv-1",
		TurnID:         "turn-1",
		Assistant:      "steward",
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
	require.NotNil(t, recorder.lastTurn.AgentIDUsed)
	require.Equal(t, "intake_sidecar", *recorder.lastTurn.AgentIDUsed)
}

var _ apiconv.Client = (*recordingConvClient)(nil)
