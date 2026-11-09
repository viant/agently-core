package sdk

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func standaloneIDs(messages []*TurnMessageState) []string {
	var ids []string
	for _, message := range messages {
		ids = append(ids, message.MessageID)
	}
	return ids
}

func TestStandaloneAssistantMessagesUseNativeOwnership(t *testing.T) {
	require.Empty(t, SelectStandaloneAssistantMessages(nil))
	message := func(id, role, mode string) *TurnMessageState {
		return &TurnMessageState{MessageID: id, Role: role, Mode: mode, Content: "Same public text."}
	}
	first, second := message("first", "assistant", "task"), message("second", "assistant", "")
	turn := &TurnState{TurnID: "turn", Messages: []*TurnMessageState{
		nil, first, message("user", "user", ""), message("internal", "assistant", " ROUTER "),
		message("page", "assistant", "task"), message("model", "assistant", "task"),
		message("page-narration", "assistant", "task"), message("page-final", "assistant", "task"),
		message("narration", "assistant", "task"), message("final", "assistant", "task"),
		message("tool", "assistant", "task"), message("", "assistant", "task"),
		{MessageID: "interim", Role: "assistant", Interim: 1, Content: "Interim text."},
		second, first,
	}, Assistant: &AssistantState{
		Narration: &AssistantMessageState{MessageID: "narration"}, Final: &AssistantMessageState{MessageID: "final"},
	}, Execution: &ExecutionState{Pages: []*ExecutionPageState{nil, {
		AssistantMessageID: "page", NarrationMessageID: "page-narration", FinalAssistantMessageID: "page-final",
		ModelSteps: []*ModelStepState{nil, {AssistantMessageID: "model"}},
		ToolSteps:  []*ToolStepState{nil, {ToolMessageID: "tool"}},
	}}}}
	before, err := json.Marshal(turn)
	require.NoError(t, err)
	require.Equal(t, []*TurnMessageState{first, second, first}, SelectStandaloneAssistantMessages(turn), "selection preserves every eligible occurrence in canonical order")
	after, err := json.Marshal(turn)
	require.NoError(t, err)
	require.Equal(t, before, after, "selection must not mutate canonical state")
	projected, _ := aguiBootstrapMessages(context.Background(), nil, &ConversationStateResponse{Conversation: &ConversationState{ConversationID: "example", Turns: []*TurnState{turn}}}, nil)
	var ids []string
	for _, m := range projected {
		metadata, _ := m["metadata"].(map[string]any)
		agently, _ := metadata["agently"].(map[string]any)
		presentation, _ := agently["presentation"].(map[string]any)
		if presentation["messageKind"] == "standalone" {
			ids = append(ids, m["id"].(string))
		}
	}
	require.Equal(t, []string{"first", "second"}, ids)
}

func TestNativeBootstrapDuplicateStandaloneUsesLastMetadata(t *testing.T) {
	for _, acrossTurns := range []bool{false, true} {
		t.Run(map[bool]string{false: "same_turn", true: "later_turn"}[acrossTurns], func(t *testing.T) {
			first := &TurnMessageState{MessageID: "public-note", Role: "assistant", Mode: "task", Content: "First version."}
			last := &TurnMessageState{MessageID: "public-note", Role: "assistant", Content: "Last version."}
			turns := []*TurnState{{TurnID: "first-turn", Messages: []*TurnMessageState{first, last}}}
			lastTurnID := "first-turn"
			if acrossTurns {
				turns[0].Messages = []*TurnMessageState{first}
				lastTurnID = "last-turn"
				turns = append(turns, &TurnState{TurnID: lastTurnID, Messages: []*TurnMessageState{last}})
			}
			transcript := &ConversationStateResponse{Conversation: &ConversationState{ConversationID: "example", Turns: turns}}
			before, err := json.Marshal(transcript)
			require.NoError(t, err)
			projected, quality := aguiBootstrapMessages(context.Background(), nil, transcript, nil)
			require.Len(t, projected, 1)
			require.Equal(t, "Last version.", projected[0]["content"])
			metadata := projected[0]["metadata"].(map[string]any)["agently"].(map[string]any)["presentation"].(map[string]any)
			require.Equal(t, "", metadata["mode"], "metadata, like content, must use the last eligible occurrence")
			require.Equal(t, lastTurnID, metadata["nativeTurnId"])
			require.Equal(t, "standalone", metadata["messageKind"])
			require.False(t, quality.Lossless)
			require.Equal(t, []string{"public-note"}, quality.UnavailableMessageIDs)
			after, err := json.Marshal(transcript)
			require.NoError(t, err)
			require.Equal(t, before, after)
		})
	}
}

func TestNativeBootstrapExclusionsApplyAcrossTurns(t *testing.T) {
	for _, tc := range []struct {
		name  string
		later *TurnState
	}{
		{"internal_message", &TurnState{Messages: []*TurnMessageState{{MessageID: "blocked", Role: "assistant", Mode: " ROUTER "}}}},
		{"tool_result", &TurnState{Execution: &ExecutionState{Pages: []*ExecutionPageState{{ToolSteps: []*ToolStepState{nil, {ToolMessageID: "blocked"}}}}}}},
		{"internal_page", &TurnState{Execution: &ExecutionState{Pages: []*ExecutionPageState{nil, {Mode: "router", AssistantMessageID: "blocked"}}}}},
		{"internal_model", &TurnState{Execution: &ExecutionState{Pages: []*ExecutionPageState{{ModelSteps: []*ModelStepState{nil, {Mode: "router", AssistantMessageID: "blocked"}}}}}}},
		{"internal_execution_role", &TurnState{Execution: &ExecutionState{Pages: []*ExecutionPageState{{ModelSteps: []*ModelStepState{{ExecutionRole: "router", AssistantMessageID: "blocked"}}}}}}},
		{"model_in_internal_page", &TurnState{Execution: &ExecutionState{Pages: []*ExecutionPageState{{Mode: "router", ModelSteps: []*ModelStepState{{AssistantMessageID: "blocked"}}}}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			first := &TurnState{TurnID: "first", Messages: []*TurnMessageState{
				{MessageID: "blocked", Role: "assistant", Content: "Internal text."},
				{MessageID: "kept", Role: "assistant", Content: "Public text."},
			}}
			projected, _ := aguiBootstrapMessages(context.Background(), nil, &ConversationStateResponse{Conversation: &ConversationState{
				ConversationID: "example", Turns: []*TurnState{nil, first, tc.later},
			}}, nil)
			require.Len(t, projected, 1)
			require.Equal(t, "kept", projected[0]["id"])
			require.Equal(t, "Public text.", projected[0]["content"])
		})
	}
}
