package sdk

import (
	"testing"
	"time"

	"github.com/viant/agently-core/runtime/streaming"
)

func TestReduce_ModelPayloadIDsArePreserved(t *testing.T) {
	now := time.Date(2026, 3, 26, 13, 0, 0, 0, time.UTC)
	state := Reduce(nil, &streaming.Event{
		Type:                     streaming.EventTypeModelStarted,
		ConversationID:           "conv-1",
		TurnID:                   "turn-1",
		AssistantMessageID:       "msg-1",
		Status:                   "thinking",
		CreatedAt:                now,
		RequestPayloadID:         "req-1",
		ProviderRequestPayloadID: "preq-1",
		StreamPayloadID:          "stream-1",
		Model: &streaming.EventModel{
			Provider: "openai",
			Model:    "gpt-5.4",
		},
	})
	state = Reduce(state, &streaming.Event{
		Type:                      streaming.EventTypeModelCompleted,
		ConversationID:            "conv-1",
		TurnID:                    "turn-1",
		AssistantMessageID:        "msg-1",
		Status:                    "completed",
		CreatedAt:                 now.Add(2 * time.Second),
		ResponsePayloadID:         "resp-1",
		ProviderResponsePayloadID: "presp-1",
	})

	if state == nil || len(state.Turns) != 1 {
		t.Fatalf("expected one turn, got %#v", state)
	}
	execution := state.Turns[0].Execution
	if execution == nil || len(execution.Pages) != 1 || len(execution.Pages[0].ModelSteps) != 1 {
		t.Fatalf("expected one model step, got %#v", execution)
	}
	step := execution.Pages[0].ModelSteps[0]
	if step.RequestPayloadID != "req-1" {
		t.Fatalf("expected request payload id, got %q", step.RequestPayloadID)
	}
	if step.ResponsePayloadID != "resp-1" {
		t.Fatalf("expected response payload id, got %q", step.ResponsePayloadID)
	}
	if step.ProviderRequestPayloadID != "preq-1" {
		t.Fatalf("expected provider request payload id, got %q", step.ProviderRequestPayloadID)
	}
	if step.ProviderResponsePayloadID != "presp-1" {
		t.Fatalf("expected provider response payload id, got %q", step.ProviderResponsePayloadID)
	}
	if step.StreamPayloadID != "stream-1" {
		t.Fatalf("expected stream payload id, got %q", step.StreamPayloadID)
	}
}

func TestReduce_AssistantFinalPreservesMarkdownBoundaries(t *testing.T) {
	now := time.Date(2026, 3, 31, 20, 0, 0, 0, time.UTC)
	content := "0 recommendations saved for team review.\n\n## Highlights\n| A | B |\n|---|---|\n| 1 | 2 |\n"

	state := Reduce(nil, &streaming.Event{
		Type:               streaming.EventTypeAssistantFinal,
		ConversationID:     "conv-1",
		TurnID:             "turn-1",
		AssistantMessageID: "msg-1",
		Content:            content,
		CreatedAt:          now,
	})

	if state == nil || len(state.Turns) != 1 {
		t.Fatalf("expected one turn, got %#v", state)
	}
	got := state.Turns[0].Assistant.Final.Content
	if got != content {
		t.Fatalf("expected assistant final content to preserve whitespace boundaries\nwant: %q\ngot:  %q", content, got)
	}
	page := state.Turns[0].Execution.Pages[0]
	if page.Content != content {
		t.Fatalf("expected page content to preserve whitespace boundaries\nwant: %q\ngot:  %q", content, page.Content)
	}
}

func TestReduce_ModelCompletedPreservesMarkdownBoundaries(t *testing.T) {
	now := time.Date(2026, 3, 31, 20, 1, 0, 0, time.UTC)
	preamble := "Next best action\n\n"
	content := "CSV\n\n## Supporting Metrics\n| Metric | Value |\n|---|---|\n| a | b |\n"

	state := Reduce(nil, &streaming.Event{
		Type:               streaming.EventTypeModelStarted,
		ConversationID:     "conv-1",
		TurnID:             "turn-1",
		AssistantMessageID: "msg-1",
		Status:             "thinking",
		CreatedAt:          now,
	})
	state = Reduce(state, &streaming.Event{
		Type:               streaming.EventTypeModelCompleted,
		ConversationID:     "conv-1",
		TurnID:             "turn-1",
		AssistantMessageID: "msg-1",
		Status:             "completed",
		Preamble:           preamble,
		Content:            content,
		CreatedAt:          now.Add(time.Second),
		FinalResponse:      true,
	})

	page := state.Turns[0].Execution.Pages[0]
	if page.Preamble != preamble {
		t.Fatalf("expected preamble to preserve whitespace boundaries\nwant: %q\ngot:  %q", preamble, page.Preamble)
	}
	if page.Content != content {
		t.Fatalf("expected content to preserve whitespace boundaries\nwant: %q\ngot:  %q", content, page.Content)
	}
}

func TestReduce_FeedLifecycle(t *testing.T) {
	state := Reduce(nil, &streaming.Event{
		Type:           streaming.EventTypeToolFeedActive,
		ConversationID: "conv-1",
		FeedID:         "plan",
		FeedTitle:      "Plan",
		FeedItemCount:  3,
		FeedData: map[string]any{
			"foo": "bar",
		},
	})
	if state == nil || len(state.Feeds) != 1 {
		t.Fatalf("expected one feed, got %#v", state)
	}
	if state.Feeds[0].FeedID != "plan" {
		t.Fatalf("expected feed id plan, got %q", state.Feeds[0].FeedID)
	}
	if state.Feeds[0].Title != "Plan" {
		t.Fatalf("expected feed title Plan, got %q", state.Feeds[0].Title)
	}
	if state.Feeds[0].ItemCount != 3 {
		t.Fatalf("expected feed item count 3, got %d", state.Feeds[0].ItemCount)
	}

	state = Reduce(state, &streaming.Event{
		Type:           streaming.EventTypeToolFeedActive,
		ConversationID: "conv-1",
		FeedID:         "plan",
		FeedItemCount:  5,
	})
	if len(state.Feeds) != 1 {
		t.Fatalf("expected one feed after update, got %#v", state.Feeds)
	}
	if state.Feeds[0].ItemCount != 5 {
		t.Fatalf("expected updated item count 5, got %d", state.Feeds[0].ItemCount)
	}

	state = Reduce(state, &streaming.Event{
		Type:           streaming.EventTypeToolFeedInactive,
		ConversationID: "conv-1",
		FeedID:         "plan",
	})
	if len(state.Feeds) != 0 {
		t.Fatalf("expected no feeds after inactive, got %#v", state.Feeds)
	}
}

func TestReduce_TextDeltaMarksModelStepStreaming(t *testing.T) {
	now := time.Date(2026, 4, 3, 16, 0, 0, 0, time.UTC)
	state := Reduce(nil, &streaming.Event{
		Type:               streaming.EventTypeModelStarted,
		ConversationID:     "conv-1",
		TurnID:             "turn-1",
		AssistantMessageID: "msg-1",
		Status:             "thinking",
		CreatedAt:          now,
	})
	state = Reduce(state, &streaming.Event{
		Type:               streaming.EventTypeTextDelta,
		ConversationID:     "conv-1",
		TurnID:             "turn-1",
		AssistantMessageID: "msg-1",
		Content:            "Hello",
		CreatedAt:          now.Add(time.Second),
	})

	page := state.Turns[0].Execution.Pages[0]
	if page.Content != "Hello" {
		t.Fatalf("expected text delta content to accumulate, got %q", page.Content)
	}
	if len(page.ModelSteps) != 1 {
		t.Fatalf("expected one model step, got %#v", page.ModelSteps)
	}
	if page.ModelSteps[0].Status != "streaming" {
		t.Fatalf("expected model step status streaming, got %q", page.ModelSteps[0].Status)
	}
}

func TestReduce_ToolCompletedFallsBackToCreatedAtForCompletedAt(t *testing.T) {
	now := time.Date(2026, 4, 3, 16, 1, 0, 0, time.UTC)
	state := Reduce(nil, &streaming.Event{
		Type:           streaming.EventTypeToolCallStarted,
		ConversationID: "conv-1",
		TurnID:         "turn-1",
		ToolCallID:     "call-1",
		ToolMessageID:  "tool-msg-1",
		ToolName:       "resources/read",
		Status:         "running",
		CreatedAt:      now,
	})
	state = Reduce(state, &streaming.Event{
		Type:           streaming.EventTypeToolCallCompleted,
		ConversationID: "conv-1",
		TurnID:         "turn-1",
		ToolCallID:     "call-1",
		ToolMessageID:  "tool-msg-1",
		ToolName:       "resources/read",
		Status:         "completed",
		CreatedAt:      now.Add(2 * time.Second),
	})

	page := state.Turns[0].Execution.Pages[0]
	if len(page.ToolSteps) != 1 {
		t.Fatalf("expected one tool step, got %#v", page.ToolSteps)
	}
	if page.ToolSteps[0].CompletedAt == nil || !page.ToolSteps[0].CompletedAt.Equal(now.Add(2*time.Second)) {
		t.Fatalf("expected completedAt fallback to createdAt, got %#v", page.ToolSteps[0].CompletedAt)
	}
}

func TestReduce_AsyncToolLifecycleAttachesOperation(t *testing.T) {
	now := time.Date(2026, 4, 8, 10, 0, 0, 0, time.UTC)
	state := Reduce(nil, &streaming.Event{
		Type:           streaming.EventTypeToolCallStarted,
		ConversationID: "conv-1",
		TurnID:         "turn-1",
		ToolCallID:     "call-1",
		ToolMessageID:  "tool-msg-1",
		ToolName:       "llm/agents:start",
		Status:         "running",
		CreatedAt:      now,
	})
	state = Reduce(state, &streaming.Event{
		Type:           streaming.EventTypeToolCallWaiting,
		ConversationID: "conv-1",
		TurnID:         "turn-1",
		ToolCallID:     "call-1",
		ToolName:       "llm/agents:status",
		OperationID:    "child-1",
		Status:         "running",
		Content:        "still working",
		CreatedAt:      now.Add(time.Second),
	})
	state = Reduce(state, &streaming.Event{
		Type:           streaming.EventTypeToolCallCompleted,
		ConversationID: "conv-1",
		TurnID:         "turn-1",
		ToolCallID:     "call-1",
		ToolName:       "llm/agents:status",
		OperationID:    "child-1",
		Status:         "completed",
		ResponsePayload: map[string]interface{}{
			"items": []map[string]interface{}{{"conversationId": "child-1", "status": "completed"}},
		},
		CreatedAt: now.Add(2 * time.Second),
	})

	page := state.Turns[0].Execution.Pages[0]
	requireStep := page.ToolSteps[0]
	if requireStep.OperationID != "child-1" {
		t.Fatalf("expected operation id child-1, got %q", requireStep.OperationID)
	}
	if requireStep.AsyncOperation == nil {
		t.Fatalf("expected async operation state")
	}
	if requireStep.AsyncOperation.Status != "completed" {
		t.Fatalf("expected async operation completed, got %q", requireStep.AsyncOperation.Status)
	}
	if requireStep.AsyncOperation.Message != "still working" {
		t.Fatalf("expected async message, got %q", requireStep.AsyncOperation.Message)
	}
	if len(requireStep.AsyncOperation.Response) == 0 {
		t.Fatalf("expected async response payload")
	}
}

func TestReduce_AsyncToolLifecycleTerminalStates(t *testing.T) {
	testCases := []struct {
		name      string
		eventType streaming.EventType
		status    string
		errorMsg  string
	}{
		{
			name:      "failed",
			eventType: streaming.EventTypeToolCallFailed,
			status:    "failed",
			errorMsg:  "boom",
		},
		{
			name:      "canceled",
			eventType: streaming.EventTypeToolCallCanceled,
			status:    "canceled",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			now := time.Date(2026, 4, 8, 11, 0, 0, 0, time.UTC)
			state := Reduce(nil, &streaming.Event{
				Type:           streaming.EventTypeToolCallStarted,
				ConversationID: "conv-1",
				TurnID:         "turn-1",
				ToolCallID:     "call-1",
				ToolMessageID:  "tool-msg-1",
				ToolName:       "system/exec:start",
				Status:         "running",
				CreatedAt:      now,
			})
			state = Reduce(state, &streaming.Event{
				Type:           streaming.EventTypeToolCallWaiting,
				ConversationID: "conv-1",
				TurnID:         "turn-1",
				ToolCallID:     "call-1",
				ToolName:       "system/exec:status",
				OperationID:    "sess-1",
				Status:         "running",
				Content:        "still working",
				CreatedAt:      now.Add(time.Second),
			})
			state = Reduce(state, &streaming.Event{
				Type:           testCase.eventType,
				ConversationID: "conv-1",
				TurnID:         "turn-1",
				ToolCallID:     "call-1",
				ToolName:       "system/exec:status",
				OperationID:    "sess-1",
				Status:         testCase.status,
				Error:          testCase.errorMsg,
				ResponsePayload: map[string]interface{}{
					"sessionId": "sess-1",
					"status":    testCase.status,
				},
				CreatedAt: now.Add(2 * time.Second),
			})

			page := state.Turns[0].Execution.Pages[0]
			requireStep := page.ToolSteps[0]
			if requireStep.OperationID != "sess-1" {
				t.Fatalf("expected operation id sess-1, got %q", requireStep.OperationID)
			}
			if requireStep.Status != testCase.status {
				t.Fatalf("expected tool step status %q, got %q", testCase.status, requireStep.Status)
			}
			if requireStep.AsyncOperation == nil {
				t.Fatalf("expected async operation state")
			}
			if requireStep.AsyncOperation.Status != testCase.status {
				t.Fatalf("expected async operation status %q, got %q", testCase.status, requireStep.AsyncOperation.Status)
			}
			if testCase.errorMsg != "" && requireStep.AsyncOperation.Error != testCase.errorMsg {
				t.Fatalf("expected async error %q, got %q", testCase.errorMsg, requireStep.AsyncOperation.Error)
			}
			if len(requireStep.AsyncOperation.Response) == 0 {
				t.Fatalf("expected async response payload")
			}
		})
	}
}

func TestReduce_ElicitationResolvedMapsCanceledStatus(t *testing.T) {
	now := time.Date(2026, 4, 3, 16, 2, 0, 0, time.UTC)
	state := Reduce(nil, &streaming.Event{
		Type:           streaming.EventTypeElicitationRequested,
		ConversationID: "conv-1",
		TurnID:         "turn-1",
		ElicitationID:  "elic-1",
		Content:        "Need input",
		CreatedAt:      now,
	})
	state = Reduce(state, &streaming.Event{
		Type:           streaming.EventTypeElicitationResolved,
		ConversationID: "conv-1",
		TurnID:         "turn-1",
		ElicitationID:  "elic-1",
		Status:         "cancelled",
		CreatedAt:      now.Add(time.Second),
	})

	if state.Turns[0].Elicitation == nil {
		t.Fatalf("expected elicitation state")
	}
	if state.Turns[0].Elicitation.Status != ElicitationStatusCanceled {
		t.Fatalf("expected canceled elicitation status, got %q", state.Turns[0].Elicitation.Status)
	}
}

func TestReduce_TurnQueuedDoesNotDowngradeTerminalTurn(t *testing.T) {
	now := time.Date(2026, 4, 3, 16, 3, 0, 0, time.UTC)
	state := &ConversationState{
		ConversationID: "conv-1",
		Turns: []*TurnState{
			{TurnID: "turn-1", Status: TurnStatusCompleted, CreatedAt: now},
		},
	}

	state = Reduce(state, &streaming.Event{
		Type:           streaming.EventTypeTurnQueued,
		ConversationID: "conv-1",
		TurnID:         "turn-1",
		CreatedAt:      now.Add(time.Second),
	})

	if state.Turns[0].Status != TurnStatusCompleted {
		t.Fatalf("expected completed turn to remain completed, got %q", state.Turns[0].Status)
	}
}
