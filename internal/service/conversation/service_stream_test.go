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

func TestLLMResponseEventFromMessage(t *testing.T) {
	msg := convcli.NewMessage()
	msg.SetId("m1")
	msg.SetConversationID("conv-1")
	msg.SetTurnID("turn-1")
	msg.SetParentMessageID("parent-1")
	msg.SetRole("assistant")
	msg.SetType("text")
	msg.SetIteration(2)
	msg.SetPreamble("Inspecting the repo")
	msg.SetContent("Final answer")
	msg.SetInterim(0)
	msg.SetStatus("completed")
	msg.SetCreatedAt(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))

	got := llmResponseEventFromMessage(msg, "conv-1")
	require.NotNil(t, got)
	require.Equal(t, streaming.EventTypeLLMResponse, got.Type)
	require.Equal(t, "m1", got.AssistantMessageID)
	require.Equal(t, "parent-1", got.ParentMessageID)
	require.Equal(t, "turn-1", got.TurnID)
	require.Equal(t, 2, got.Iteration)
	require.Equal(t, 2, got.PageIndex)
	require.Equal(t, "Inspecting the repo", got.Preamble)
	require.Equal(t, "Final answer", got.Content)
	require.True(t, got.FinalResponse)
	require.Equal(t, "completed", got.Status)
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
