package toolexec

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	convmem "github.com/viant/agently-core/app/store/data/memory"
	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/runtime/clienttool"
	memory "github.com/viant/agently-core/runtime/requestctx"
)

func TestClientToolDeferralPersistsOpenCallAndNeverExecutesBackend(t *testing.T) {
	session, err := clienttool.NewSession([]llm.ToolDefinition{{Name: "frontend/lookup"}})
	require.NoError(t, err)
	ctx := clienttool.WithSession(context.Background(), session)
	ctx = memory.WithTurnMeta(ctx, memory.TurnMeta{ConversationID: "conversation", TurnID: "turn"})
	ctx = memory.WithModelMessageID(ctx, "assistant")
	ctx = memory.WithRunMeta(ctx, memory.RunMeta{RunID: "turn", Iteration: 2})
	reg := &scriptedRegistry{preflight: []error{fmt.Errorf("must not preflight")}, script: []scriptedResult{{result: "must not execute"}}}
	conv := &stubConv{}
	step := StepInfo{ID: "call", Name: "frontend-lookup", Args: map[string]interface{}{"id": 7}}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			call, span, err := ExecuteToolStep(ctx, reg, step, conv)
			if err != nil {
				t.Error(err)
				return
			}
			if call.Result != "" || !span.EndedAt.IsZero() {
				t.Error("deferred call falsely completed")
			}
		}()
	}
	wg.Wait()
	require.Len(t, session.Pending(), 1)
	pending := session.Pending()[0]
	require.Equal(t, "assistant", pending.AssistantMessageID)
	require.Equal(t, 2, pending.Iteration)
	require.Len(t, conv.patchedPayloads, 1)
	require.Len(t, conv.patchedToolCalls, 3)
	call := conv.patchedToolCalls[2]
	require.Equal(t, "waiting_for_user", call.Status)
	require.Nil(t, call.CompletedAt)
	require.Nil(t, call.ResponsePayloadID)
	require.Equal(t, "waiting_for_user", derefString(conv.patchedMessages[len(conv.patchedMessages)-1].Status))
	require.Len(t, reg.preflight, 1)
	require.Len(t, reg.script, 1)
	require.Equal(t, pending.ToolMessageID, call.MessageID)
}

func TestClientToolDeferralPersistenceFailureIsFatal(t *testing.T) {
	session, err := clienttool.NewSession([]llm.ToolDefinition{{Name: "frontend"}})
	require.NoError(t, err)
	ctx := clienttool.WithSession(memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: "conversation", TurnID: "turn"}), session)
	conv := &stubConv{failPatchToolCallAt: map[int]error{3: fmt.Errorf("waiting patch failed")}}
	_, _, err = ExecuteToolStep(ctx, &scriptedRegistry{}, StepInfo{ID: "call", Name: "frontend", Args: map[string]interface{}{}}, conv)
	require.ErrorContains(t, err, "waiting patch failed")
	require.Error(t, session.Error())
	require.Empty(t, session.Pending())
}

func TestCompleteClientToolUpdatesOriginalCallAndRejectsConflicts(t *testing.T) {
	store := convmem.New()
	ctx := context.Background()
	conversation := apiconv.NewConversation()
	conversation.SetId("completion-conv")
	require.NoError(t, store.PatchConversations(ctx, conversation))
	turn := apiconv.NewTurn()
	turn.SetId("completion-turn")
	turn.SetConversationID("completion-conv")
	turn.SetStatus("running")
	require.NoError(t, store.PatchTurn(ctx, turn))
	session, err := clienttool.NewSession([]llm.ToolDefinition{{Name: "frontend"}})
	require.NoError(t, err)
	ctx = clienttool.WithSession(memory.WithTurnMeta(ctx, memory.TurnMeta{ConversationID: "completion-conv", TurnID: "completion-turn"}), session)
	_, _, err = ExecuteToolStep(ctx, nil, StepInfo{ID: "call", Name: "frontend", Args: map[string]interface{}{"id": 7}}, store)
	require.NoError(t, err)
	pending := session.Pending()[0]
	require.NoError(t, CompleteClientTool(ctx, store, pending, "result"))
	require.NoError(t, CompleteClientTool(ctx, store, pending, "result"))
	require.ErrorContains(t, CompleteClientTool(ctx, store, pending, "conflict"), "different result")
	message, err := store.GetMessage(ctx, pending.ToolMessageID)
	require.NoError(t, err)
	require.Equal(t, "result", message.GetContent())
	require.Equal(t, "completed", message.ToolMessage[0].ToolCall.Status)
	require.NotNil(t, message.ToolMessage[0].ToolCall.CompletedAt)
	wrong := pending
	wrong.ID = "wrong"
	require.ErrorContains(t, CompleteClientTool(ctx, store, wrong, "result"), "identity mismatch")
}

func TestCompleteClientToolMessageRejectsInvalidMediaBeforeMutation(t *testing.T) {
	store := &stubConv{}
	err := CompleteClientToolMessage(context.Background(), store, clienttool.PendingCall{}, json.RawMessage(`[{"type":"image","source":{"type":"invalid","value":"https://example.test/image"}}]`))
	require.ErrorContains(t, err, "unknown AG-UI part source")
	require.Empty(t, store.patchedMessages)
	require.Empty(t, store.patchedPayloads)
	require.Empty(t, store.patchedToolCalls)
}

func TestCompleteClientToolResultPreservesOrderedPartialMediaAndFailure(t *testing.T) {
	store := convmem.New()
	ctx := context.Background()
	conversation := apiconv.NewConversation()
	conversation.SetId("partial-conv")
	require.NoError(t, store.PatchConversations(ctx, conversation))
	turn := apiconv.NewTurn()
	turn.SetId("partial-turn")
	turn.SetConversationID("partial-conv")
	turn.SetStatus("running")
	require.NoError(t, store.PatchTurn(ctx, turn))
	session, err := clienttool.NewSession([]llm.ToolDefinition{{Name: "frontend"}})
	require.NoError(t, err)
	ctx = clienttool.WithSession(memory.WithTurnMeta(ctx, memory.TurnMeta{ConversationID: "partial-conv", TurnID: "partial-turn"}), session)
	_, _, err = ExecuteToolStep(ctx, nil, StepInfo{ID: "call", Name: "frontend", Args: map[string]interface{}{}}, store)
	require.NoError(t, err)
	pending := session.Pending()[0]
	body := json.RawMessage(`[{"type":"text","text":"partial"},{"type":"image","source":{"type":"data","value":"AQID","mimeType":"image/png"}}]`)
	require.NoError(t, CompleteClientToolResult(ctx, store, pending, body, "lookup incomplete"))
	require.NoError(t, CompleteClientToolResult(ctx, store, pending, body, "lookup incomplete"))
	message, err := store.GetMessage(ctx, pending.ToolMessageID)
	require.NoError(t, err)
	require.JSONEq(t, string(body), message.GetContent())
	require.Equal(t, clienttool.ContentMIME, *message.ContextSummary)
	require.Equal(t, "failed", message.ToolMessage[0].ToolCall.Status)
	require.Equal(t, "lookup incomplete", *message.ToolMessage[0].ToolCall.ErrorMessage)
}
