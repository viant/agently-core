package executor

import (
	"context"
	"testing"

	agconv "github.com/viant/agently-core/pkg/agently/conversation"
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

	err = adapter.Publish(context.Background(), &modelcallctx.StreamEvent{
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
	if ev.Type != streaming.EventTypeChunk {
		t.Fatalf("unexpected event type: %s", ev.Type)
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
			Type:               streaming.EventTypeLLMResponse,
			AssistantMessageID: "m1",
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
	if ev.Type != streaming.EventTypeLLMResponse {
		t.Fatalf("unexpected event type: %s", ev.Type)
	}
	if ev.ConversationID != "c1" {
		t.Fatalf("unexpected conversation id: %s", ev.ConversationID)
	}
	if len(ev.ToolCallsPlanned) != 1 || ev.ToolCallsPlanned[0].ToolName != "llm/agents/run" {
		t.Fatalf("unexpected planned tool calls: %#v", ev.ToolCallsPlanned)
	}
}
