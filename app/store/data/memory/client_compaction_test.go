package memory

import (
	"context"
	"github.com/stretchr/testify/require"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	requestctx "github.com/viant/agently-core/runtime/requestctx"
	"testing"
)

func TestPatchMessagePreservesCompactionArchiveAndSummary(t *testing.T) {
	store := New()
	ctx := context.Background()
	conv := apiconv.NewConversation()
	conv.SetId("compaction-conv")
	require.NoError(t, store.PatchConversations(ctx, conv))
	meta := requestctx.TurnMeta{ConversationID: "compaction-conv", TurnID: "compaction-turn"}
	turn := apiconv.NewTurn()
	turn.SetId(meta.TurnID)
	turn.SetConversationID(meta.ConversationID)
	require.NoError(t, store.PatchTurn(ctx, turn))
	msg, err := apiconv.AddMessage(ctx, store, &meta, apiconv.WithRole("assistant"), apiconv.WithType("text"), apiconv.WithContent("original facts"))
	require.NoError(t, err)
	patch := apiconv.NewMessage()
	patch.SetId(msg.Id)
	patch.SetArchived(1)
	patch.SetSummary("retained handoff")
	require.NoError(t, store.PatchMessage(ctx, patch))
	got, err := store.GetMessage(ctx, msg.Id)
	require.NoError(t, err)
	require.NotNil(t, got.Archived)
	require.Equal(t, 1, *got.Archived)
	require.NotNil(t, got.Summary)
	require.Equal(t, "retained handoff", *got.Summary)
}
