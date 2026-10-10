package main

import (
	"encoding/json"
	"github.com/viant/agently-core/sdk"
	"github.com/viant/agently-core/sdk/api"
	"testing"
)

func TestApprovalReceiptRequiresOriginalOperationAndCommittedContent(t *testing.T) {
	tuple := approvalTuple{PublicCallID: "public-original", MessageID: "original-effect"}
	actual := map[string]any{"values": map[string]any{"HOME": "fixture-home", "PATH": "fixture-path"}}
	makeEvents := func(call, message, content string) []json.RawMessage {
		a, _ := json.Marshal(map[string]any{"type": "TOOL_CALL_RESULT", "toolCallId": call, "messageId": message, "content": content})
		return []json.RawMessage{a, json.RawMessage(`{"type":"RUN_FINISHED","outcome":{"type":"success"}}`)}
	}
	events := makeEvents(tuple.PublicCallID, tuple.MessageID, `{"values":{"HOME":"fixture-home","PATH":"fixture-path"}}`)
	n, terminal, err := exactReceiptEvents(events, tuple, actual)
	if err != nil || n != 1 || !terminal {
		t.Fatal("original committed receipt rejected")
	}
	for _, bad := range [][]json.RawMessage{makeEvents("later-op", tuple.MessageID, `{}`), makeEvents(tuple.PublicCallID, "later-message", `{}`), makeEvents(tuple.PublicCallID, tuple.MessageID, `{"values":{"SHELL":"wrong"}}`), append(events, events[0])} {
		if _, _, err = exactReceiptEvents(bad, tuple, actual); err == nil {
			t.Fatal("foreign/different/duplicate receipt accepted")
		}
	}
}
func TestApprovalTupleRejectsForeignConversationTurnAndOwner(t *testing.T) {
	run := identity{ConversationID: "original-conversation", TurnID: "original-turn"}
	good := &sdk.PendingToolApproval{ID: "approval", ConversationID: run.ConversationID, TurnID: run.TurnID, UserID: "owner", MessageID: "effect"}
	if _, err := findExactApproval(&sdk.PendingToolApprovalPage{Rows: []*sdk.PendingToolApproval{good}}, good.ID, run); err != nil {
		t.Fatal("exact tuple rejected")
	}
	for _, change := range []func(*sdk.PendingToolApproval){func(r *sdk.PendingToolApproval) { r.ConversationID = "foreign" }, func(r *sdk.PendingToolApproval) { r.TurnID = "foreign" }, func(r *sdk.PendingToolApproval) { r.UserID = "" }, func(r *sdk.PendingToolApproval) { r.MessageID = "" }} {
		copy := *good
		change(&copy)
		if _, err := findExactApproval(&sdk.PendingToolApprovalPage{Rows: []*sdk.PendingToolApproval{&copy}}, good.ID, run); err == nil {
			t.Fatal("unproven tuple accepted")
		}
	}
}

func TestOriginalApprovalEffectIsDistinctFromQueueParent(t *testing.T) {
	snapshot := &sdk.ConversationStateResponse{Conversation: &api.ConversationState{Turns: []*api.TurnState{{TurnID: "original-turn", Execution: &api.ExecutionState{Pages: []*api.ExecutionPageState{{ToolSteps: []*api.ToolStepState{{ToolCallID: "original-op", ToolMessageID: "original-effect", ParentMessageID: "queue-assistant-parent"}, {ToolCallID: "later-op", ToolMessageID: "later-effect", ParentMessageID: "other-parent"}}}}}}}}}
	step, err := originalTool(snapshot, "original-turn", "original-op", "")
	if err != nil || step.ToolMessageID != "original-effect" || step.ParentMessageID != "queue-assistant-parent" {
		t.Fatal("original effect/parent distinction lost")
	}
	if _, err = originalTool(snapshot, "original-turn", "original-op", "queue-assistant-parent"); err == nil {
		t.Fatal("queue parent accepted as effect")
	}
	if _, err = originalTool(snapshot, "foreign-turn", "original-op", ""); err == nil {
		t.Fatal("foreign turn accepted")
	}
}
