package async

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	asynccfg "github.com/viant/agently-core/protocol/async"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
)

func TestService_List_ReturnsNonTerminalOpsForCurrentConversation(t *testing.T) {
	ctx := context.Background()
	manager := asynccfg.NewManager()
	ctx = asynccfg.WithManager(ctx, manager)
	ctx = runtimerequestctx.WithTurnMeta(ctx, runtimerequestctx.TurnMeta{
		ConversationID: "conv-1",
		TurnID:         "turn-1",
	})

	manager.Register(ctx, asynccfg.RegisterInput{
		ID:                   "op-wait",
		ParentConvID:         "conv-1",
		ParentTurnID:         "turn-1",
		ToolName:             "llm/agents:start",
		StatusToolName:       "llm/agents:status",
		StatusOperationIDArg: "conversationId",
		ExecutionMode:        string(asynccfg.ExecutionModeWait),
		Status:               "running",
		OperationIntent:      "inspect repo",
	})
	manager.Register(ctx, asynccfg.RegisterInput{
		ID:                   "op-terminal",
		ParentConvID:         "conv-1",
		ParentTurnID:         "turn-1",
		ToolName:             "llm/agents:start",
		StatusToolName:       "llm/agents:status",
		StatusOperationIDArg: "conversationId",
		ExecutionMode:        string(asynccfg.ExecutionModeWait),
		Status:               "completed",
	})
	manager.Register(ctx, asynccfg.RegisterInput{
		ID:                   "op-other-conv",
		ParentConvID:         "conv-2",
		ParentTurnID:         "turn-2",
		ToolName:             "llm/agents:start",
		StatusToolName:       "llm/agents:status",
		StatusOperationIDArg: "conversationId",
		ExecutionMode:        string(asynccfg.ExecutionModeWait),
		Status:               "running",
	})

	svc := New()
	var out ListOutput
	require.NoError(t, svc.List(ctx, &ListInput{}, &out))
	require.Len(t, out.Ops, 1)
	require.Equal(t, "op-wait", out.Ops[0].OperationID)
	require.Equal(t, "llm/agents:start", out.Ops[0].Tool)
	require.Equal(t, "llm/agents:status", out.Ops[0].StatusTool)
	require.Equal(t, "conversationId", out.Ops[0].OperationIDArg)
	require.False(t, out.Ops[0].SameToolRecall)
	require.Equal(t, "inspect repo", out.Ops[0].Intent)
}

func TestService_List_IgnoresLLMProvidedFieldsBeyondSchema(t *testing.T) {
	// Defensive: ListInput only has Tool and Mode. Even if a malicious or
	// confused LLM tried to smuggle a conversationId in the JSON, the
	// struct would not bind it and the tool would still scope to the
	// trusted context conversation.
	ctx := context.Background()
	manager := asynccfg.NewManager()
	ctx = asynccfg.WithManager(ctx, manager)
	ctx = runtimerequestctx.WithTurnMeta(ctx, runtimerequestctx.TurnMeta{
		ConversationID: "conv-trusted",
		TurnID:         "turn-1",
	})
	manager.Register(ctx, asynccfg.RegisterInput{
		ID:            "op-trusted",
		ParentConvID:  "conv-trusted",
		ParentTurnID:  "turn-1",
		ToolName:      "llm/agents:start",
		ExecutionMode: string(asynccfg.ExecutionModeWait),
		Status:        "running",
	})
	manager.Register(ctx, asynccfg.RegisterInput{
		ID:            "op-foreign",
		ParentConvID:  "conv-foreign",
		ParentTurnID:  "turn-x",
		ToolName:      "llm/agents:start",
		ExecutionMode: string(asynccfg.ExecutionModeWait),
		Status:        "running",
	})

	svc := New()
	var out ListOutput
	require.NoError(t, svc.List(ctx, &ListInput{}, &out))
	require.Len(t, out.Ops, 1)
	require.Equal(t, "op-trusted", out.Ops[0].OperationID)
}

func TestService_List_FiltersByToolAndMode(t *testing.T) {
	ctx := context.Background()
	manager := asynccfg.NewManager()
	ctx = asynccfg.WithManager(ctx, manager)
	ctx = runtimerequestctx.WithTurnMeta(ctx, runtimerequestctx.TurnMeta{
		ConversationID: "conv-1",
		TurnID:         "turn-1",
	})
	manager.Register(ctx, asynccfg.RegisterInput{
		ID:            "a",
		ParentConvID:  "conv-1",
		ParentTurnID:  "turn-1",
		ToolName:      "llm/agents:start",
		ExecutionMode: string(asynccfg.ExecutionModeWait),
		Status:        "running",
	})
	manager.Register(ctx, asynccfg.RegisterInput{
		ID:            "b",
		ParentConvID:  "conv-1",
		ParentTurnID:  "turn-1",
		ToolName:      "system/exec:execute",
		ExecutionMode: string(asynccfg.ExecutionModeDetach),
		Status:        "running",
	})

	svc := New()

	var byTool ListOutput
	require.NoError(t, svc.List(ctx, &ListInput{Tool: "llm/agents:start"}, &byTool))
	require.Len(t, byTool.Ops, 1)
	require.Equal(t, "a", byTool.Ops[0].OperationID)

	var byMode ListOutput
	require.NoError(t, svc.List(ctx, &ListInput{Mode: "detach"}, &byMode))
	require.Len(t, byMode.Ops, 1)
	require.Equal(t, "b", byMode.Ops[0].OperationID)
}

func TestService_List_NoManagerReturnsEmpty(t *testing.T) {
	ctx := context.Background()
	ctx = runtimerequestctx.WithTurnMeta(ctx, runtimerequestctx.TurnMeta{
		ConversationID: "conv-1",
		TurnID:         "turn-1",
	})
	svc := New()
	var out ListOutput
	require.NoError(t, svc.List(ctx, &ListInput{}, &out))
	require.Empty(t, out.Ops)
}

func TestService_List_NoTurnContextReturnsEmpty(t *testing.T) {
	ctx := context.Background()
	manager := asynccfg.NewManager()
	ctx = asynccfg.WithManager(ctx, manager)
	manager.Register(ctx, asynccfg.RegisterInput{
		ID:            "a",
		ParentConvID:  "conv-1",
		ExecutionMode: string(asynccfg.ExecutionModeWait),
		Status:        "running",
	})

	svc := New()
	var out ListOutput
	require.NoError(t, svc.List(ctx, &ListInput{}, &out))
	require.Empty(t, out.Ops, "without a trusted conversation id, the tool must refuse to return anything")
}
