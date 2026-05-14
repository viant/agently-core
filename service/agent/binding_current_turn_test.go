package agent

import (
	"context"
	"testing"
	"time"

	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/protocol/binding"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/service/core"
)

func TestAppendCurrentMessages_AdvancesAnchorToCurrentTurn(t *testing.T) {
	baseTime := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)
	history := &binding.History{
		Past: []*binding.Turn{{
			ID: "turn-old",
			Messages: []*binding.Message{{
				ID:        "user-old",
				Kind:      binding.MessageKindChatUser,
				Role:      "user",
				Content:   "show my order 2667545",
				CreatedAt: baseTime.Add(-10 * time.Minute),
			}},
		}},
		CurrentTurnID: "turn-current",
		Current: &binding.Turn{
			ID: "turn-current",
		},
		LastResponse: &binding.Trace{
			ID:   "resp-old",
			Kind: binding.KindResponse,
			At:   baseTime.Add(-5 * time.Minute),
		},
		Traces: map[string]*binding.Trace{
			binding.KindResponse.Key("resp-old"): {
				ID:   "resp-old",
				Kind: binding.KindResponse,
				At:   baseTime.Add(-5 * time.Minute),
			},
		},
	}
	appendCurrentMessages(history, &binding.Message{
		ID:          "tool-current",
		Kind:        binding.MessageKindToolResult,
		Role:        string(llm.RoleAssistant),
		ToolOpID:    "call-current",
		ToolName:    "ui_view-open",
		ToolArgs:    map[string]any{"id": "order", "parameters": map[string]any{"AdOrderId": []any{2667545}}},
		Content:     `{"ok":false,"error":"no active ui client attached"}`,
		CreatedAt:   baseTime,
		ToolTraceID: "resp-current",
	})

	if history.LastResponse == nil || history.LastResponse.ID != "resp-current" {
		t.Fatalf("expected current turn trace to become last response, got %#v", history.LastResponse)
	}
	if got := history.Traces[binding.KindToolCall.Key("call-current")]; got == nil || got.ID != "resp-current" {
		t.Fatalf("expected current replay tool trace to map to resp-current, got %#v", got)
	}

	req := &llm.GenerateRequest{
		Messages: history.LLMMessages(),
	}
	ctx := runtimerequestctx.WithTurnMeta(context.Background(), runtimerequestctx.TurnMeta{
		TurnID:         "turn-current",
		ConversationID: "conv-1",
	})
	cont := (&core.Service{}).BuildContinuationRequest(ctx, req, history)
	if cont == nil {
		t.Fatalf("expected continuation request")
	}
	if cont.PreviousResponseID != "resp-current" {
		t.Fatalf("expected previous_response_id resp-current, got %q", cont.PreviousResponseID)
	}
	if len(cont.Messages) != 2 {
		t.Fatalf("expected assistant/tool replay pair, got %d messages", len(cont.Messages))
	}
	if len(cont.Messages[0].ToolCalls) != 1 || cont.Messages[0].ToolCalls[0].ID != "call-current" {
		t.Fatalf("expected replayed assistant tool call for call-current, got %#v", cont.Messages[0].ToolCalls)
	}
	if cont.Messages[1].ToolCallId != "call-current" {
		t.Fatalf("expected tool result for call-current, got %#v", cont.Messages[1])
	}
}
