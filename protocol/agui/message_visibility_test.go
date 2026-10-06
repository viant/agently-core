package agui

import (
	"encoding/json"
	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/runtime/streaming"
	"testing"
)

func TestTranslatorDoesNotEmitInternalBodiesForStandardClients(t *testing.T) {
	translator := NewTranslator("thread", "run")
	start := translator.Translate(&streaming.Event{Type: streaming.EventTypeModelStarted, ConversationID: "thread", TurnID: "turn", AssistantMessageID: "internal", ModelCallID: "call", Mode: "router"})
	require.NotEmpty(t, start)
	hidden := translator.Translate(&streaming.Event{Type: streaming.EventTypeTextDelta, ConversationID: "thread", TurnID: "turn", AssistantMessageID: "internal", Content: `{"classification":true}`})
	for _, event := range hidden {
		require.NotEqual(t, "TEXT_MESSAGE_CONTENT", event.Type)
	}
	visible := translator.Translate(&streaming.Event{Type: streaming.EventTypeTextDelta, ConversationID: "thread", TurnID: "turn", AssistantMessageID: "task", Mode: "task", Content: `{"classification":true}`})
	encoded, err := json.Marshal(visible)
	require.NoError(t, err)
	require.Contains(t, string(encoded), "TEXT_MESSAGE_CONTENT")
	complete := translator.Translate(&streaming.Event{Type: streaming.EventTypeModelCompleted, ConversationID: "thread", TurnID: "turn", AssistantMessageID: "internal", ModelCallID: "call", Mode: "router", Content: "private prose"})
	encoded, err = json.Marshal(complete)
	require.NoError(t, err)
	require.Contains(t, string(encoded), "STEP_FINISHED")
	require.NotContains(t, string(encoded), "private prose")
}
