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

var _ apiconv.Client = (*recordingConvClient)(nil)
