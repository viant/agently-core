package memory

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/runtime/requestctx"
)

func TestSparseToolRequestPayloadLinkPreservesOperationIdentity(t *testing.T) {
	ctx := context.Background()
	store := New()
	conv := apiconv.NewConversation()
	conv.SetId("sparse-tool")
	require.NoError(t, store.PatchConversations(ctx, conv))
	turn := apiconv.NewTurn()
	turn.SetId("turn")
	turn.SetConversationID(conv.Id)
	require.NoError(t, store.PatchTurn(ctx, turn))
	msg, err := apiconv.AddMessage(ctx, store, &requestctx.TurnMeta{ConversationID: conv.Id, TurnID: "turn"}, apiconv.WithRole("tool"), apiconv.WithType("tool_op"))
	require.NoError(t, err)
	link := apiconv.NewToolCall()
	link.SetMessageID(msg.Id)
	requestID := "request"
	link.RequestPayloadID = &requestID
	link.Has.RequestPayloadID = true
	require.Error(t, store.PatchToolCall(ctx, link), "new tool requires an operation identity")
	call := apiconv.NewToolCall()
	call.SetMessageID(msg.Id)
	call.SetOpID("operation")
	call.SetToolName("fixture")
	call.SetStatus("running")
	require.NoError(t, store.PatchToolCall(ctx, call))
	require.NoError(t, store.PatchToolCall(ctx, link))
	got, err := store.GetConversation(ctx, conv.Id, apiconv.WithIncludeToolCall(true))
	require.NoError(t, err)
	tc := got.Transcript[0].Message[0].ToolMessage[0].ToolCall
	require.Equal(t, "operation", tc.OpId)
	require.Equal(t, "running", tc.Status)
	require.Equal(t, "request", *tc.RequestPayloadId)
}
