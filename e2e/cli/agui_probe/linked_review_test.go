package main

import (
	"github.com/viant/agently-core/sdk"
	"github.com/viant/agently-core/sdk/api"
	"testing"
	"time"
)

func TestLinkedReviewEvidenceBindsExactObservedTurnAndCompletedEffect(t *testing.T) {
	now := time.Now()
	snapshot := &sdk.ConversationStateResponse{Conversation: &api.ConversationState{Turns: []*api.TurnState{{TurnID: "original-turn", Assistant: &api.AssistantState{Final: &api.AssistantMessageState{MessageID: "actual-final", CreatedAt: now.Add(time.Second)}}, Execution: &api.ExecutionState{Pages: []*api.ExecutionPageState{{ToolSteps: []*api.ToolStepState{{ToolName: "llm/agents/run", ToolCallID: "original-op", ToolMessageID: "effect-not-parent", ParentMessageID: "parent", Status: "completed", LinkedConversationID: "actual-child", RequestPayloadID: "actual-request", CompletedAt: &now}}}}}}}}}
	step, _, err := linkedReviewStep(snapshot, "original-turn", "llm/agents/run")
	if err != nil || step.ToolMessageID != "effect-not-parent" {
		t.Fatal("actual effect not bound", err)
	}
	if _, _, err = linkedReviewStep(snapshot, "foreign-turn", "llm/agents/run"); err == nil {
		t.Fatal("foreign turn accepted")
	}
	snapshot.Conversation.Turns[0].Assistant.Final.CreatedAt = now.Add(-time.Second)
	if _, _, err = linkedReviewStep(snapshot, "original-turn", "llm/agents/run"); err == nil {
		t.Fatal("final before tool accepted")
	}
}
