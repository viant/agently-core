package sdk

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/store/data"
	convw "github.com/viant/agently-core/pkg/agently/conversation/write"
	asynccfg "github.com/viant/agently-core/protocol/async"
)

func TestBackendClient_ListAsyncOperations_ReturnsConversationScopedPendingOps(t *testing.T) {
	ctx := context.Background()
	dataSvc, err := data.NewThinServiceInMemory(ctx)
	require.NoError(t, err)
	_, err = dataSvc.PatchConversations(ctx, []*convw.Conversation{
		convw.NewMutableConversationView(convw.WithConversationID("conv-1")),
	})
	require.NoError(t, err)

	manager := asynccfg.NewManager()
	_, _ = manager.Register(ctx, asynccfg.RegisterInput{
		ID:                   "op-1",
		ParentConvID:         "conv-1",
		ParentTurnID:         "turn-1",
		ToolName:             "system/exec:execute",
		StatusToolName:       "system/exec:execute",
		StatusOperationIDArg: "sessionId",
		ExecutionMode:        string(asynccfg.ExecutionModeDetach),
		Status:               "running",
	})
	_, _ = manager.Register(ctx, asynccfg.RegisterInput{
		ID:                   "op-2",
		ParentConvID:         "conv-1",
		ParentTurnID:         "turn-1",
		ToolName:             "llm/agents:start",
		StatusToolName:       "llm/agents:status",
		StatusOperationIDArg: "conversationId",
		ExecutionMode:        string(asynccfg.ExecutionModeWait),
		Status:               "completed",
	})
	_, _ = manager.Register(ctx, asynccfg.RegisterInput{
		ID:                   "op-3",
		ParentConvID:         "conv-2",
		ParentTurnID:         "turn-9",
		ToolName:             "system/exec:execute",
		StatusToolName:       "system/exec:execute",
		StatusOperationIDArg: "sessionId",
		ExecutionMode:        string(asynccfg.ExecutionModeDetach),
		Status:               "running",
	})

	client := &backendClient{
		data:         dataSvc,
		asyncManager: manager,
	}
	out, err := client.ListAsyncOperations(ctx, &ListAsyncOperationsInput{
		ConversationID: "conv-1",
		Tool:           "system/exec:execute",
		Mode:           "detach",
	})
	require.NoError(t, err)
	require.Len(t, out.Ops, 1)
	require.Equal(t, "op-1", out.Ops[0].OperationID)
	require.Equal(t, "system/exec:execute", out.Ops[0].Tool)
	require.Equal(t, "detach", out.Ops[0].ExecutionMode)
}
