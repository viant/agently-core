package reactor

import (
	"context"
	"testing"

	"github.com/viant/agently-core/genai/llm"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
)

func TestResumePlan_ReusesDurableResultsWithoutModelCall(t *testing.T) {
	s := &Service{turnToolResults: map[string][]llm.ToolCall{}}
	resp := &llm.GenerateResponse{ResponseID: "r1", Choices: []llm.Choice{{Message: llm.Message{ToolCalls: []llm.ToolCall{{ID: "op-a", Name: "t/a"}}}}}}
	plan := s.PlanFromResponse(resp)
	if len(plan.Steps) != 1 || plan.Steps[0].ID != "op-a" || plan.Steps[0].ResponseID != "r1" {
		t.Fatalf("unexpected plan: %+v", plan.Steps)
	}
	ctx := runtimerequestctx.WithTurnMeta(context.Background(), runtimerequestctx.TurnMeta{TurnID: "turn-1"})
	out, err := s.ResumePlan(ctx, plan, map[string]llm.ToolCall{"op-a": {ID: "op-a", Name: "t/a", Result: "done"}})
	if err != nil || out == nil || len(out.Steps) != 1 {
		t.Fatalf("ResumePlan err=%v out=%+v", err, out)
	}
	results := s.TurnToolResults("turn-1")
	if len(results) != 1 || results[0].Result != "done" {
		t.Fatalf("durable result not remembered: %+v", results)
	}
}
