package message

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	convmem "github.com/viant/agently-core/app/store/data/memory"
	"github.com/viant/agently-core/runtime/recovery"
	requestctx "github.com/viant/agently-core/runtime/requestctx"
)

func TestProactiveRemoveValidatesAllTuplesBeforeAnyMutation(t *testing.T) {
	for _, badKind := range []string{"outside", "latest-user", "summary", "pending", "other-conversation", "duplicate", "empty-summary"} {
		t.Run(badKind, func(t *testing.T) {
			store := convmem.New()
			meta := requestctx.TurnMeta{ConversationID: "scope-conv", TurnID: "scope-turn"}
			ctx := requestctx.WithTurnMeta(requestctx.WithConversationID(context.Background(), meta.ConversationID), meta)
			conv := apiconv.NewConversation()
			conv.SetId(meta.ConversationID)
			require.NoError(t, store.PatchConversations(ctx, conv))
			turn := apiconv.NewTurn()
			turn.SetId(meta.TurnID)
			turn.SetConversationID(meta.ConversationID)
			require.NoError(t, store.PatchTurn(ctx, turn))
			old, err := apiconv.AddMessage(ctx, store, &meta, apiconv.WithRole("user"), apiconv.WithType("text"), apiconv.WithContent("older facts"))
			require.NoError(t, err)
			latest, err := apiconv.AddMessage(ctx, store, &meta, apiconv.WithRole("user"), apiconv.WithType("text"), apiconv.WithContent("latest request"))
			require.NoError(t, err)
			summary, err := apiconv.AddMessage(ctx, store, &meta, apiconv.WithRole("assistant"), apiconv.WithType("text"), apiconv.WithStatus("summary"), apiconv.WithContent("protected summary"))
			require.NoError(t, err)
			pending, err := apiconv.AddMessage(ctx, store, &meta, apiconv.WithRole("assistant"), apiconv.WithType("tool_op"), apiconv.WithStatus("running"), apiconv.WithContent("pending operation"))
			require.NoError(t, err)
			otherMeta := requestctx.TurnMeta{ConversationID: "other-conv", TurnID: "other-turn"}
			other := apiconv.NewConversation()
			other.SetId(otherMeta.ConversationID)
			require.NoError(t, store.PatchConversations(ctx, other))
			otherTurn := apiconv.NewTurn()
			otherTurn.SetId(otherMeta.TurnID)
			otherTurn.SetConversationID(otherMeta.ConversationID)
			require.NoError(t, store.PatchTurn(ctx, otherTurn))
			cross, err := apiconv.AddMessage(ctx, store, &otherMeta, apiconv.WithRole("assistant"), apiconv.WithType("text"), apiconv.WithContent("other conversation facts"))
			require.NoError(t, err)
			badID := uuid.NewString()
			switch badKind {
			case "latest-user":
				badID = latest.Id
			case "summary":
				badID = summary.Id
			case "pending":
				badID = pending.Id
			case "other-conversation":
				badID = cross.Id
			case "duplicate":
				badID = old.Id
			}
			scope := &recovery.CompactionScope{ConversationID: meta.ConversationID, EligibleIDs: map[string]bool{old.Id: true, badID: true}}
			ctx = recovery.WithProactive(ctx, scope)
			secondSummary := "summary"
			if badKind == "empty-summary" {
				secondSummary = ""
				badID = old.Id
			}
			in := &RemoveInput{Tuples: []RemoveTuple{{MessageIds: []string{old.Id}, Summary: "valid handoff"}, {MessageIds: []string{badID}, Summary: secondSummary}}}
			require.Error(t, New(store).remove(ctx, in, &RemoveOutput{}))
			got, err := store.GetConversation(ctx, meta.ConversationID, apiconv.WithIncludeToolCall(true))
			require.NoError(t, err)
			count := 0
			for _, turn := range got.GetTranscript() {
				for _, m := range turn.Message {
					count++
					require.True(t, m.Archived == nil || *m.Archived == 0)
				}
			}
			require.Equal(t, 4, count)
		})
	}
}

func TestProactiveRemovePreservesAuthoritativeOperationIdentity(t *testing.T) {
	store := convmem.New()
	meta := requestctx.TurnMeta{ConversationID: "operation-handoff", TurnID: "operation-turn"}
	ctx := requestctx.WithTurnMeta(requestctx.WithConversationID(context.Background(), meta.ConversationID), meta)
	conv := apiconv.NewConversation()
	conv.SetId(meta.ConversationID)
	require.NoError(t, store.PatchConversations(ctx, conv))
	turn := apiconv.NewTurn()
	turn.SetId(meta.TurnID)
	turn.SetConversationID(meta.ConversationID)
	require.NoError(t, store.PatchTurn(ctx, turn))
	completed, err := apiconv.AddMessage(ctx, store, &meta, apiconv.WithRole("tool"), apiconv.WithType("tool_op"), apiconv.WithContent("verified account"))
	require.NoError(t, err)
	call := apiconv.NewToolCall()
	call.SetMessageID(completed.Id)
	call.SetTurnID(meta.TurnID)
	call.SetOpID("lookup-done-718")
	call.SetToolName("account-lookup")
	call.SetStatus("succeeded")
	require.NoError(t, store.PatchToolCall(ctx, call))
	pending, err := apiconv.AddMessage(ctx, store, &meta, apiconv.WithRole("tool"), apiconv.WithType("tool_op"), apiconv.WithStatus("running"), apiconv.WithContent("still running"))
	require.NoError(t, err)
	latest, err := apiconv.AddMessage(ctx, store, &meta, apiconv.WithRole("user"), apiconv.WithType("text"), apiconv.WithContent("continue"))
	require.NoError(t, err)
	ctx = recovery.WithProactive(ctx, &recovery.CompactionScope{ConversationID: meta.ConversationID, EligibleIDs: map[string]bool{completed.Id: true}})
	out := &RemoveOutput{}
	require.NoError(t, New(store).remove(ctx, &RemoveInput{Tuples: []RemoveTuple{{MessageIds: []string{completed.Id}, Summary: "The lookup operation was wrong-id and is still pending."}}}, out))
	require.Equal(t, 1, out.ArchivedMessages)
	fresh, err := store.GetConversation(ctx, meta.ConversationID, apiconv.WithIncludeToolCall(true))
	require.NoError(t, err)
	for _, tr := range fresh.GetTranscript() {
		for _, m := range tr.Message {
			switch m.Id {
			case completed.Id:
				require.NotNil(t, m.Archived)
				require.Equal(t, 1, *m.Archived)
				require.Equal(t, "lookup-done-718", m.ToolMessage[0].ToolCall.OpId)
			case pending.Id, latest.Id:
				require.True(t, m.Archived == nil || *m.Archived == 0)
			case out.CreatedSummaryMessageIds[0]:
				require.NotNil(t, m.Content)
				require.Contains(t, *m.Content, `"operationId":"lookup-done-718"`)
				require.Contains(t, *m.Content, `"status":"succeeded"`)
				require.Contains(t, *m.Content, `"tool":"account-lookup"`)
				require.Contains(t, *m.Content, "override conflicting summary text")
			}
		}
	}
}
