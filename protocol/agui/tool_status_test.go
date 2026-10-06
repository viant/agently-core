package agui

import (
	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/runtime/streaming"
	"testing"
	"time"
)

func TestQueuedToolPresentationDoesNotCompleteExecution(t *testing.T) {
	for _, status := range []string{"queued", "pending", "waiting", "waiting_for_user", "blocked"} {
		t.Run(status, func(t *testing.T) {
			translator := NewTranslator("thread", "run")
			event := &streaming.Event{Type: streaming.EventTypeToolCallCompleted, ToolCallID: "op", ToolName: "tool", AssistantMessageID: "assistant", ToolMessageID: "tool-message", Status: status, Arguments: map[string]any{"n": 1}, Content: "queued for user approval", ResponsePayload: map[string]any{"presentation": "queue"}}
			events := translator.Translate(event)
			types := []string{}
			for _, event := range events {
				types = append(types, event.Type)
			}
			require.Contains(t, types, "TOOL_CALL_END")
			require.NotContains(t, types, "TOOL_CALL_RESULT")
			now := time.Now()
			event.Status = "completed"
			event.CompletedAt = &now
			event.Content = "actual execution result"
			event.ResponsePayload = nil
			events = translator.Translate(event)
			require.Equal(t, []string{"TOOL_CALL_RESULT", "ACTIVITY_SNAPSHOT"}, kinds(events))
			require.Equal(t, "TOOL_CALL_RESULT", events[0].Type)
			require.Equal(t, "actual execution result", *events[0].Content)
			assertToolExecutionPresentation(t, events[1], "op", "completed")
			require.Empty(t, translator.Translate(event), "completed replay must not duplicate result")
		})
	}
}
