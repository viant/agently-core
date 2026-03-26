package conversation

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	convcli "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/runtime/memory"
	"github.com/viant/agently-core/runtime/streaming"
)

func TestMessagePatchPayload(t *testing.T) {
	msg := convcli.NewMessage()
	msg.SetId("m1")
	msg.SetStatus("completed")
	msg.SetToolName("llm/agents-run")
	msg.SetInterim(0)
	msg.SetPreamble("delegating")
	msg.SetLinkedConversationID("child-123")
	msg.SetParentMessageID("parent-1")
	msg.SetTurnID("turn-1")
	msg.SetContent("done")
	msg.SetRole("assistant")
	msg.SetMode("summary")
	msg.SetType("text")
	msg.SetSequence(7)
	msg.SetIteration(2)
	msg.SetCreatedAt(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))

	got := messagePatchPayload(msg)
	require.EqualValues(t, "completed", got["status"])
	require.EqualValues(t, "llm/agents/run", got["toolName"])
	require.EqualValues(t, 0, got["interim"])
	require.EqualValues(t, "delegating", got["preamble"])
	require.EqualValues(t, "child-123", got["linkedConversationId"])
	require.EqualValues(t, "parent-1", got["parentMessageId"])
	require.EqualValues(t, "turn-1", got["turnId"])
	require.EqualValues(t, "done", got["content"])
	require.EqualValues(t, "assistant", got["role"])
	require.EqualValues(t, "summary", got["mode"])
	require.EqualValues(t, "text", got["messageType"])
	require.EqualValues(t, 7, got["sequence"])
	require.EqualValues(t, 2, got["iteration"])
	require.EqualValues(t, "2026-01-02T03:04:05Z", got["createdAt"])
}

func TestMessagePatchPayload_Empty(t *testing.T) {
	msg := convcli.NewMessage()
	msg.SetId("m1")
	msg.SetConversationID("c1")
	msg.SetRole("assistant")
	msg.SetType("text")

	got := messagePatchPayload(msg)
	require.EqualValues(t, "assistant", got["role"])
	require.EqualValues(t, "text", got["messageType"])
}

func TestPublishTurnEvent_RunningTurnPublishesStartedControl(t *testing.T) {
	bus := streaming.NewMemoryBus(4)
	svc := &Service{streamPub: bus}
	sub, err := bus.Subscribe(context.Background(), nil)
	require.NoError(t, err)
	defer sub.Close()

	turn := convcli.NewTurn()
	turn.SetId("turn-1")
	turn.SetConversationID("conv-1")
	turn.SetStatus("running")
	turn.SetRunID("run-1")
	turn.SetAgentIDUsed("steward-performance")
	turn.SetCreatedAt(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))

	svc.publishTurnEvent(context.Background(), turn)

	select {
	case ev := <-sub.C():
		require.NotNil(t, ev)
		require.Equal(t, streaming.EventTypeControl, ev.Type)
		require.Equal(t, "turn_started", ev.Op)
		require.Equal(t, "turn-1", ev.ID)
		require.Equal(t, "conv-1", ev.StreamID)
		require.EqualValues(t, "turn-1", ev.Patch["turnId"])
		require.EqualValues(t, "running", ev.Patch["status"])
		require.EqualValues(t, "run-1", ev.Patch["runId"])
		require.EqualValues(t, "steward-performance", ev.Patch["agentIdUsed"])
		require.EqualValues(t, "2026-01-02T03:04:05Z", ev.Patch["createdAt"])
	case <-time.After(2 * time.Second):
		t.Fatal("expected turn_started event")
	}

	select {
	case ev := <-sub.C():
		require.NotNil(t, ev)
		require.Equal(t, streaming.EventTypeTurnStarted, ev.Type)
		require.Equal(t, "turn-1", ev.TurnID)
		require.Equal(t, "conv-1", ev.ConversationID)
		require.Equal(t, "steward-performance", ev.AgentIDUsed)
		require.Equal(t, "running", ev.Status)
	case <-time.After(2 * time.Second):
		t.Fatal("expected typed turn_started event")
	}
}

func TestPublishTurnEvent_SucceededTurnPublishesCompleted(t *testing.T) {
	bus := streaming.NewMemoryBus(2)
	svc := &Service{streamPub: bus}
	sub, err := bus.Subscribe(context.Background(), nil)
	require.NoError(t, err)
	defer sub.Close()

	turn := convcli.NewTurn()
	turn.SetId("turn-1")
	turn.SetConversationID("conv-1")
	turn.SetStatus("succeeded")
	turn.SetCreatedAt(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))

	svc.publishTurnEvent(context.Background(), turn)

	select {
	case ev := <-sub.C():
		require.NotNil(t, ev)
		require.Equal(t, streaming.EventTypeTurnCompleted, ev.Type)
		require.Equal(t, "turn-1", ev.TurnID)
		require.Equal(t, "conv-1", ev.ConversationID)
		require.Equal(t, "succeeded", ev.Status)
	case <-time.After(2 * time.Second):
		t.Fatal("expected turn_completed event")
	}
}

func TestEmitCanonicalModelEvent_ThinkingPublishesModelStarted(t *testing.T) {
	bus := streaming.NewMemoryBus(4)
	svc := &Service{streamPub: bus}
	sub, err := bus.Subscribe(context.Background(), nil)
	require.NoError(t, err)
	defer sub.Close()

	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{
		TurnID:         "turn-1",
		ConversationID: "conv-parent",
	})

	mc := convcli.NewModelCall()
	mc.SetMessageID("mc-1")
	mc.SetTurnID("turn-1")
	mc.SetProvider("openai")
	mc.SetModel("gpt-5.2")
	mc.SetStatus("thinking")

	svc.emitCanonicalModelEvent(ctx, mc)

	select {
	case ev := <-sub.C():
		require.NotNil(t, ev)
		require.Equal(t, streaming.EventTypeModelStarted, ev.Type)
		require.Equal(t, "conv-parent", ev.ConversationID)
		require.Equal(t, "conv-parent", ev.StreamID)
		require.Equal(t, "turn-1", ev.TurnID)
		require.Equal(t, "mc-1", ev.AssistantMessageID)
		require.Equal(t, "openai", ev.Model.Provider)
		require.Equal(t, "gpt-5.2", ev.Model.Model)
	case <-time.After(2 * time.Second):
		t.Fatal("expected model_started event")
	}
}

func TestEmitCanonicalModelEvent_CompletedPublishesModelCompleted(t *testing.T) {
	bus := streaming.NewMemoryBus(4)
	svc := &Service{streamPub: bus}
	sub, err := bus.Subscribe(context.Background(), nil)
	require.NoError(t, err)
	defer sub.Close()

	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{
		TurnID:         "turn-1",
		ConversationID: "conv-parent",
	})

	mc := convcli.NewModelCall()
	mc.SetMessageID("mc-1")
	mc.SetTurnID("turn-1")
	mc.SetProvider("openai")
	mc.SetModel("gpt-5.2")
	mc.SetStatus("completed")

	svc.emitCanonicalModelEvent(ctx, mc)

	select {
	case ev := <-sub.C():
		require.NotNil(t, ev)
		require.Equal(t, streaming.EventTypeModelCompleted, ev.Type)
		require.Equal(t, "conv-parent", ev.ConversationID)
		require.Equal(t, "turn-1", ev.TurnID)
		require.Equal(t, "mc-1", ev.AssistantMessageID)
		require.NotNil(t, ev.CompletedAt)
	case <-time.After(2 * time.Second):
		t.Fatal("expected model_completed event")
	}
}

func TestEmitCanonicalModelEvent_ToolCallOnlyResponse_StillEmits(t *testing.T) {
	// Verify that model_started and model_completed are emitted even when
	// the LLM response contains only tool calls and no text content/preamble.
	bus := streaming.NewMemoryBus(4)
	svc := &Service{streamPub: bus}
	sub, err := bus.Subscribe(context.Background(), nil)
	require.NoError(t, err)
	defer sub.Close()

	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{
		TurnID:         "turn-1",
		ConversationID: "conv-parent",
	})

	// Step 1: model_started (thinking) — no content, no preamble
	mc := convcli.NewModelCall()
	mc.SetMessageID("mc-1")
	mc.SetTurnID("turn-1")
	mc.SetProvider("openai")
	mc.SetModel("gpt-5.2")
	mc.SetStatus("thinking")

	svc.emitCanonicalModelEvent(ctx, mc)

	select {
	case ev := <-sub.C():
		require.Equal(t, streaming.EventTypeModelStarted, ev.Type)
		require.Equal(t, "conv-parent", ev.StreamID)
	case <-time.After(2 * time.Second):
		t.Fatal("expected model_started for tool-call-only response")
	}

	// Step 2: model_completed — no content/preamble/finalResponse
	mc2 := convcli.NewModelCall()
	mc2.SetMessageID("mc-1")
	mc2.SetTurnID("turn-1")
	mc2.SetProvider("openai")
	mc2.SetModel("gpt-5.2")
	mc2.SetStatus("completed")
	// Note: no content, no preamble — tool-call-only response

	svc.emitCanonicalModelEvent(ctx, mc2)

	select {
	case ev := <-sub.C():
		require.Equal(t, streaming.EventTypeModelCompleted, ev.Type)
		require.Equal(t, "conv-parent", ev.StreamID)
		require.Equal(t, "turn-1", ev.TurnID)
		// No final response data — this is a tool-call-only completion
		require.False(t, ev.FinalResponse)
		require.Empty(t, ev.Content)
	case <-time.After(2 * time.Second):
		t.Fatal("expected model_completed for tool-call-only response")
	}
}

func TestToolCallEvent_WaitingForUserIsNonTerminal(t *testing.T) {
	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{
		TurnID:          "turn-1",
		ConversationID:  "conv-parent",
		ParentMessageID: "assistant-1",
	})
	tc := convcli.NewToolCall()
	tc.SetMessageID("tool-msg-1")
	tc.SetOpID("op-1")
	tc.SetToolName("resources/grepFiles")
	tc.SetStatus("waiting_for_user")
	tc.SetTurnID("turn-1")

	ev := toolCallEvent(ctx, tc)
	require.NotNil(t, ev)
	require.Equal(t, streaming.EventTypeToolCallStarted, ev.Type)
	require.Equal(t, "waiting_for_user", ev.Status)
}

func TestEmitCanonicalModelEvent_NoConversationID_Skips(t *testing.T) {
	bus := streaming.NewMemoryBus(4)
	svc := &Service{streamPub: bus}
	sub, err := bus.Subscribe(context.Background(), nil)
	require.NoError(t, err)
	defer sub.Close()

	// Context with no TurnMeta and no ConversationID
	ctx := context.Background()

	mc := convcli.NewModelCall()
	mc.SetMessageID("mc-1")
	mc.SetStatus("thinking")

	svc.emitCanonicalModelEvent(ctx, mc)

	select {
	case ev := <-sub.C():
		t.Fatalf("should not emit when conversationID is empty, got type=%s", ev.Type)
	case <-time.After(100 * time.Millisecond):
		// expected — no event
	}
}

func TestEmitCanonicalAssistantEvents_PreamblePublishesPreamble(t *testing.T) {
	bus := streaming.NewMemoryBus(4)
	svc := &Service{streamPub: bus}
	sub, err := bus.Subscribe(context.Background(), nil)
	require.NoError(t, err)
	defer sub.Close()

	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{
		TurnID:         "turn-1",
		ConversationID: "conv-1",
	})

	msg := convcli.NewMessage()
	msg.SetId("msg-1")
	msg.SetRole("assistant")
	msg.SetPreamble("Let me analyze...")
	msg.SetInterim(1) // still interim — preamble phase
	msg.SetContent("")
	msg.SetTurnID("turn-1")

	svc.emitCanonicalAssistantEvents(ctx, msg, "conv-1")

	select {
	case ev := <-sub.C():
		require.Equal(t, streaming.EventTypeAssistantPreamble, ev.Type)
		require.Equal(t, "conv-1", ev.ConversationID)
		require.Equal(t, "turn-1", ev.TurnID)
		require.Equal(t, "msg-1", ev.AssistantMessageID)
		require.Equal(t, "Let me analyze...", ev.Content)
	case <-time.After(2 * time.Second):
		t.Fatal("expected assistant_preamble event")
	}
}

func TestEmitCanonicalAssistantEvents_FinalPublishesFinal(t *testing.T) {
	bus := streaming.NewMemoryBus(4)
	svc := &Service{streamPub: bus}
	sub, err := bus.Subscribe(context.Background(), nil)
	require.NoError(t, err)
	defer sub.Close()

	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{
		TurnID:         "turn-1",
		ConversationID: "conv-1",
	})

	msg := convcli.NewMessage()
	msg.SetId("msg-1")
	msg.SetRole("assistant")
	msg.SetContent("Here is the answer.")
	msg.SetInterim(0) // final
	msg.SetTurnID("turn-1")

	svc.emitCanonicalAssistantEvents(ctx, msg, "conv-1")

	select {
	case ev := <-sub.C():
		require.Equal(t, streaming.EventTypeAssistantFinal, ev.Type)
		require.Equal(t, "conv-1", ev.ConversationID)
		require.Equal(t, "turn-1", ev.TurnID)
		require.Equal(t, "msg-1", ev.AssistantMessageID)
		require.Equal(t, "Here is the answer.", ev.Content)
		require.True(t, ev.FinalResponse)
	case <-time.After(2 * time.Second):
		t.Fatal("expected assistant_final event")
	}
}

func TestEmitCanonicalAssistantEvents_ToolCallOnlyNoEvents(t *testing.T) {
	// When the assistant message has no preamble, no content, and is interim,
	// no assistant_preamble or assistant_final should be emitted.
	bus := streaming.NewMemoryBus(4)
	svc := &Service{streamPub: bus}
	sub, err := bus.Subscribe(context.Background(), nil)
	require.NoError(t, err)
	defer sub.Close()

	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{
		TurnID:         "turn-1",
		ConversationID: "conv-1",
	})

	msg := convcli.NewMessage()
	msg.SetId("msg-1")
	msg.SetRole("assistant")
	msg.SetInterim(1)
	// No preamble, no content — tool-call-only with no explanation

	svc.emitCanonicalAssistantEvents(ctx, msg, "conv-1")

	select {
	case ev := <-sub.C():
		t.Fatalf("should not emit assistant events for empty content, got type=%s", ev.Type)
	case <-time.After(100 * time.Millisecond):
		// expected — no event
	}
}

func TestPatchToolCallPublishesTypedTimelineEvent(t *testing.T) {
	bus := streaming.NewMemoryBus(2)
	sub, err := bus.Subscribe(context.Background(), nil)
	require.NoError(t, err)
	defer sub.Close()

	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{
		TurnID:          "turn-1",
		ConversationID:  "conv-1",
		ParentMessageID: "parent-1",
	})
	ctx = context.WithValue(ctx, memory.ModelMessageIDKey, "assistant-1")

	call := convcli.NewToolCall()
	call.SetMessageID("tool-msg-1")
	call.SetOpID("tool-call-1")
	call.SetTurnID("turn-1")
	call.SetToolName("llm/agents-run")
	call.SetStatus("running")
	call.SetIteration(3)
	reqID := "req-1"
	call.RequestPayloadID = &reqID
	call.Has.RequestPayloadID = true

	got := toolCallEvent(ctx, call)
	require.NotNil(t, got)
	require.Equal(t, streaming.EventTypeToolCallStarted, got.Type)
	require.Equal(t, "assistant-1", got.AssistantMessageID)
	require.Equal(t, "tool-call-1", got.ToolCallID)
	require.Equal(t, "tool-msg-1", got.ToolMessageID)
	require.Equal(t, "llm/agents/run", got.ToolName)
	require.Equal(t, 3, got.Iteration)
}

func TestToolCallEvent_CompletedFallsBackToContextTurnIDAndKeepsOpID(t *testing.T) {
	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{
		TurnID:          "turn-ctx",
		ConversationID:  "conv-1",
		ParentMessageID: "parent-1",
	})
	ctx = context.WithValue(ctx, memory.ModelMessageIDKey, "assistant-1")

	call := convcli.NewToolCall()
	call.SetMessageID("tool-msg-1")
	call.SetOpID("tool-call-1")
	call.SetToolName("llm/agents-run")
	call.SetStatus("completed")

	got := toolCallEvent(ctx, call)
	require.NotNil(t, got)
	require.Equal(t, streaming.EventTypeToolCallCompleted, got.Type)
	require.Equal(t, "turn-ctx", got.TurnID)
	require.Equal(t, "tool-call-1", got.ToolCallID)
	require.Equal(t, "tool-msg-1", got.ToolMessageID)
	require.Equal(t, "assistant-1", got.AssistantMessageID)
}

func TestEmitCanonicalModelEvent_FallsBackToContextTurnID(t *testing.T) {
	bus := streaming.NewMemoryBus(2)
	svc := &Service{streamPub: bus}
	sub, err := bus.Subscribe(context.Background(), nil)
	require.NoError(t, err)
	defer sub.Close()

	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{
		TurnID:         "turn-ctx",
		ConversationID: "conv-1",
	})

	mc := convcli.NewModelCall()
	mc.SetMessageID("mc-1")
	mc.SetStatus("thinking")

	svc.emitCanonicalModelEvent(ctx, mc)

	select {
	case ev := <-sub.C():
		require.Equal(t, streaming.EventTypeModelStarted, ev.Type)
		require.Equal(t, "turn-ctx", ev.TurnID)
	case <-time.After(2 * time.Second):
		t.Fatal("expected model_started event")
	}
}

func TestPublishMessagePatchEvent_SuppressesToolMessages(t *testing.T) {
	bus := streaming.NewMemoryBus(2)
	svc := &Service{streamPub: bus}
	sub, err := bus.Subscribe(context.Background(), nil)
	require.NoError(t, err)
	defer sub.Close()

	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{
		TurnID:         "turn-1",
		ConversationID: "conv-1",
	})

	msg := convcli.NewMessage()
	msg.SetId("tool-msg-1")
	msg.SetConversationID("conv-1")
	msg.SetRole("tool")
	msg.SetType("tool_op")
	msg.SetStatus("completed")

	svc.publishMessagePatchEvent(ctx, msg)

	select {
	case ev := <-sub.C():
		t.Fatalf("expected no control event for tool message patch, got type=%s op=%s", ev.Type, ev.Op)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestPublishMessagePatchEvent_SuppressesCurrentToolMessageSparsePatch(t *testing.T) {
	bus := streaming.NewMemoryBus(2)
	svc := &Service{streamPub: bus}
	sub, err := bus.Subscribe(context.Background(), nil)
	require.NoError(t, err)
	defer sub.Close()

	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{
		TurnID:         "turn-1",
		ConversationID: "conv-1",
	})
	ctx = memory.WithToolMessageID(ctx, "tool-msg-1")

	msg := convcli.NewMessage()
	msg.SetId("tool-msg-1")
	msg.SetConversationID("conv-1")
	msg.SetStatus("completed")

	svc.publishMessagePatchEvent(ctx, msg)

	select {
	case ev := <-sub.C():
		t.Fatalf("expected no control event for sparse tool message patch, got type=%s op=%s", ev.Type, ev.Op)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestPublishMessagePatchEvent_SuppressesToolStatusMessages(t *testing.T) {
	bus := streaming.NewMemoryBus(2)
	svc := &Service{streamPub: bus}
	sub, err := bus.Subscribe(context.Background(), nil)
	require.NoError(t, err)
	defer sub.Close()

	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{
		TurnID:         "turn-1",
		ConversationID: "conv-1",
	})

	msg := convcli.NewMessage()
	msg.SetId("status-msg-1")
	msg.SetConversationID("conv-1")
	msg.SetTurnID("turn-1")
	msg.SetRole("assistant")
	msg.SetMode("exec")
	msg.SetCreatedByUserID("tool")
	msg.SetToolName("llm/agents/run")
	msg.SetLinkedConversationID("child-1")

	svc.publishMessagePatchEvent(ctx, msg)

	select {
	case ev := <-sub.C():
		t.Fatalf("expected no control event for tool status message, got type=%s op=%s", ev.Type, ev.Op)
	case <-time.After(100 * time.Millisecond):
	}
}
