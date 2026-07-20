package streaming

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/sdk/rendering"
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

func TestEventRenderedContentUsesCanonicalSDKObjectOnWire(t *testing.T) {
	event := &Event{
		Type: EventTypeAssistant,
		RenderedContent: &rendering.RenderedContent{
			SchemaVersion: "1",
			Parts: []*rendering.RenderedContentPart{
				{Kind: "markdown", Text: "Ready"},
			},
		},
	}

	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	var envelope map[string]json.RawMessage
	if err = json.Unmarshal(payload, &envelope); err != nil {
		t.Fatalf("decode event envelope: %v", err)
	}
	rendered := bytes.TrimSpace(envelope["renderedContent"])
	if len(rendered) == 0 || rendered[0] != '{' {
		t.Fatalf("renderedContent must be an object, got %s", rendered)
	}
	var decoded rendering.RenderedContent
	if err = json.Unmarshal(rendered, &decoded); err != nil {
		t.Fatalf("decode renderedContent: %v", err)
	}
	if decoded.SchemaVersion != "1" || len(decoded.Parts) != 1 || decoded.Parts[0].Text != "Ready" {
		t.Fatalf("unexpected renderedContent: %+v", decoded)
	}
}
