package executor

import (
	"context"
	"testing"

	agconv "github.com/viant/agently-core/pkg/agently/conversation"
	"github.com/viant/agently-core/runtime/memory"
	"github.com/viant/agently-core/runtime/streaming"
	modelcallctx "github.com/viant/agently-core/service/core/modelcall"
)

func TestStreamPublisherAdapterPublish(t *testing.T) {
	bus := streaming.NewMemoryBus(8)
	sub, err := bus.Subscribe(context.Background(), nil)
	if err != nil {
		t.Fatalf("subscribe failed: %v", err)
	}
	defer sub.Close()

	adapter := newStreamPublisherAdapter(bus)
	if adapter == nil {
		t.Fatalf("adapter should not be nil")
	}

	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{
		TurnID:          "turn-1",
		Assistant:       "steward",
		ConversationID:  "c1",
		ParentMessageID: "user-1",
	})
	ctx = memory.WithRequestMode(ctx, "chat")

	err = adapter.Publish(ctx, &modelcallctx.StreamEvent{
		ConversationID: "c1",
		Message:        &agconv.MessageView{Id: "m1"},
		Content:        map[string]interface{}{"delta": "hello"},
	})
	if err != nil {
		t.Fatalf("publish failed: %v", err)
	}

	ev := <-sub.C()
	if ev == nil {
		t.Fatalf("expected event")
	}
	if ev.StreamID != "c1" {
		t.Fatalf("unexpected stream id: %s", ev.StreamID)
	}
	if ev.ConversationID != "c1" {
		t.Fatalf("unexpected conversation id: %s", ev.ConversationID)
	}
	if ev.Type != streaming.EventTypeTextDelta {
		t.Fatalf("unexpected event type: %s", ev.Type)
	}
	if ev.TurnID != "turn-1" {
		t.Fatalf("unexpected turn id: %s", ev.TurnID)
	}
	if ev.MessageID != "m1" {
		t.Fatalf("unexpected message id: %s", ev.MessageID)
	}
	if ev.AgentIDUsed != "steward" {
		t.Fatalf("unexpected agent id: %s", ev.AgentIDUsed)
	}
	if ev.UserMessageID != "user-1" {
		t.Fatalf("unexpected user message id: %s", ev.UserMessageID)
	}
	if ev.ParentMessageID != "user-1" {
		t.Fatalf("unexpected parent message id: %s", ev.ParentMessageID)
	}
	if ev.ModelCallID != "m1" {
		t.Fatalf("unexpected model call id: %s", ev.ModelCallID)
	}
	if ev.Mode != "chat" {
		t.Fatalf("unexpected mode: %s", ev.Mode)
	}
	if ev.Content != "hello" {
		t.Fatalf("unexpected content: %q", ev.Content)
	}
}

func TestStreamPublisherAdapterPublish_TimelineEventPassthrough(t *testing.T) {
	bus := streaming.NewMemoryBus(8)
	sub, err := bus.Subscribe(context.Background(), nil)
	if err != nil {
		t.Fatalf("subscribe failed: %v", err)
	}
	defer sub.Close()

	adapter := newStreamPublisherAdapter(bus)
	err = adapter.Publish(context.Background(), &modelcallctx.StreamEvent{
		ConversationID: "c1",
		Event: &streaming.Event{
			Type:               streaming.EventTypeToolCallWaiting,
			AssistantMessageID: "m1",
			OperationID:        "op-1",
			ToolCallsPlanned: []streaming.PlannedToolCall{
				{ToolCallID: "tc1", ToolName: "llm/agents/run"},
			},
		},
	})
	if err != nil {
		t.Fatalf("publish failed: %v", err)
	}

	ev := <-sub.C()
	if ev == nil {
		t.Fatalf("expected event")
	}
	if ev.Type != streaming.EventTypeToolCallWaiting {
		t.Fatalf("unexpected event type: %s", ev.Type)
	}
	if ev.ConversationID != "c1" {
		t.Fatalf("unexpected conversation id: %s", ev.ConversationID)
	}
	if ev.MessageID != "m1" {
		t.Fatalf("unexpected message id: %s", ev.MessageID)
	}
	if len(ev.ToolCallsPlanned) != 1 || ev.ToolCallsPlanned[0].ToolName != "llm/agents/run" {
		t.Fatalf("unexpected planned tool calls: %#v", ev.ToolCallsPlanned)
	}
	if ev.OperationID != "op-1" {
		t.Fatalf("unexpected operation id: %s", ev.OperationID)
	}
}

func TestStreamPublisherAdapterPublish_FailedAndCanceledPassthrough(t *testing.T) {
	bus := streaming.NewMemoryBus(8)
	sub, err := bus.Subscribe(context.Background(), nil)
	if err != nil {
		t.Fatalf("subscribe failed: %v", err)
	}
	defer sub.Close()

	adapter := newStreamPublisherAdapter(bus)
	err = adapter.Publish(context.Background(), &modelcallctx.StreamEvent{
		ConversationID: "c1",
		Event: &streaming.Event{
			Type:               streaming.EventTypeToolCallFailed,
			AssistantMessageID: "m1",
			OperationID:        "op-fail",
			Error:              "boom",
		},
	})
	if err != nil {
		t.Fatalf("publish failed: %v", err)
	}
	ev := <-sub.C()
	if ev == nil || ev.Type != streaming.EventTypeToolCallFailed {
		t.Fatalf("expected failed tool-call event, got %#v", ev)
	}
	if ev.OperationID != "op-fail" || ev.Error != "boom" {
		t.Fatalf("unexpected failed event payload: %#v", ev)
	}

	err = adapter.Publish(context.Background(), &modelcallctx.StreamEvent{
		ConversationID: "c1",
		Event: &streaming.Event{
			Type:               streaming.EventTypeToolCallCanceled,
			AssistantMessageID: "m2",
			OperationID:        "op-cancel",
			Status:             "canceled",
		},
	})
	if err != nil {
		t.Fatalf("publish failed: %v", err)
	}
	ev = <-sub.C()
	if ev == nil || ev.Type != streaming.EventTypeToolCallCanceled {
		t.Fatalf("expected canceled tool-call event, got %#v", ev)
	}
	if ev.OperationID != "op-cancel" || ev.Status != "canceled" {
		t.Fatalf("unexpected canceled event payload: %#v", ev)
	}
}
