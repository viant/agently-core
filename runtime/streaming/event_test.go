package streaming

import (
	"testing"

	"github.com/viant/agently-core/genai/llm"
)

func TestFromLLMEvent_TextDeltaCarriesConversationID(t *testing.T) {
	ev := FromLLMEvent("conv-1", llm.StreamEvent{
		ItemID: "msg-1",
		Kind:   llm.StreamEventTextDelta,
		Delta:  "hello",
	})

	if ev == nil {
		t.Fatalf("expected event")
	}
	if ev.Type != EventTypeTextDelta {
		t.Fatalf("unexpected type: %s", ev.Type)
	}
	if ev.StreamID != "conv-1" {
		t.Fatalf("unexpected stream id: %s", ev.StreamID)
	}
	if ev.ConversationID != "conv-1" {
		t.Fatalf("unexpected conversation id: %s", ev.ConversationID)
	}
	if ev.MessageID != "msg-1" {
		t.Fatalf("unexpected message id: %s", ev.MessageID)
	}
	if ev.Content != "hello" {
		t.Fatalf("unexpected content: %q", ev.Content)
	}
}
