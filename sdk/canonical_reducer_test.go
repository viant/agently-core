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
