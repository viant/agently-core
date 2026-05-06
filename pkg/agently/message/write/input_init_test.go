package write

import "testing"

func TestInput_mergeMissingFieldsFromCurrent(t *testing.T) {
	currentTurn := "turn-1"
	currentParent := "parent-1"
	currentMode := "task"
	currentPhase := "intake"
	currentIteration := 3

	current := &Message{
		Id:              "msg-1",
		ConversationID:  "conv-1",
		TurnID:          &currentTurn,
		ParentMessageID: &currentParent,
		Role:            "assistant",
		Type:            "text",
		Mode:            &currentMode,
		Phase:           &currentPhase,
		Iteration:       &currentIteration,
	}

	patch := &Message{}
	patch.SetId("msg-1")
	patch.SetStatus("completed")

	input := &Input{
		CurMessageById: map[string]*Message{
			"msg-1": current,
		},
	}

	input.mergeMissingFieldsFromCurrent(patch)

	if patch.ConversationID != "conv-1" {
		t.Fatalf("expected ConversationID to be merged, got %q", patch.ConversationID)
	}
	if patch.TurnID == nil || *patch.TurnID != "turn-1" {
		t.Fatalf("expected TurnID to be merged, got %#v", patch.TurnID)
	}
	if patch.ParentMessageID == nil || *patch.ParentMessageID != "parent-1" {
		t.Fatalf("expected ParentMessageID to be merged, got %#v", patch.ParentMessageID)
	}
	if patch.Role != "assistant" {
		t.Fatalf("expected Role to be merged, got %q", patch.Role)
	}
	if patch.Type != "text" {
		t.Fatalf("expected Type to be merged, got %q", patch.Type)
	}
	if patch.Mode == nil || *patch.Mode != "task" {
		t.Fatalf("expected Mode to be merged, got %#v", patch.Mode)
	}
	if patch.Phase == nil || *patch.Phase != "intake" {
		t.Fatalf("expected Phase to be merged, got %#v", patch.Phase)
	}
	if patch.Iteration == nil || *patch.Iteration != 3 {
		t.Fatalf("expected Iteration to be merged, got %#v", patch.Iteration)
	}
	if patch.Status == nil || *patch.Status != "completed" {
		t.Fatalf("expected explicit Status to remain set, got %#v", patch.Status)
	}
}
