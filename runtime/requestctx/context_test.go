package requestctx

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConversationAndTurnMetaContext(t *testing.T) {
	ctx := WithConversationID(context.Background(), "conv-1")
	ctx = WithTurnMeta(ctx, TurnMeta{ConversationID: "conv-1", TurnID: "turn-1"})
	require.Equal(t, "conv-1", ConversationIDFromContext(ctx))
	turn, ok := TurnMetaFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, "turn-1", turn.TurnID)
}

func TestRunMetaAndRequestModeContext(t *testing.T) {
	ctx := WithRunMeta(context.Background(), RunMeta{RunID: "run-1", Iteration: 2})
	ctx = WithRequestMode(ctx, "plan")
	run, ok := RunMetaFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, "run-1", run.RunID)
	require.Equal(t, 2, run.Iteration)
	require.Equal(t, "plan", RequestModeFromContext(ctx))
}
