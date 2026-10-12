package sdk

import (
	"context"
	"github.com/stretchr/testify/require"
	convstore "github.com/viant/agently-core/app/store/conversation"
	conversationmodel "github.com/viant/agently-core/model/conversation"
	"testing"
	"time"
)

func TestRunningCanonicalExecutionDoesNotClaimPublicStandaloneMessage(t *testing.T) {
	now := time.Date(2026, 10, 12, 8, 47, 0, 0, time.UTC)
	task, empty, note, final := "task", "", "Synthetic public findings", "Synthetic final report"
	noteID := "57df514f-25ac-4f86-b1b7-67a1a8fc007f"
	model2ID := "186683d0-45f3-421b-a83e-cb2b30cdce66"
	one, two, three := 1, 2, 3
	messages := []*conversationmodel.MessageView{
		{Id: "first-model", Iteration: &one, Role: "assistant", Mode: &task, Content: &empty, CreatedAt: now, ModelCall: &conversationmodel.ModelCallView{MessageId: "first-model", Status: "completed"}},
		// Actual78 message/add-produced public message has no ModelCall.
		{Id: noteID, Role: "assistant", Mode: &task, Content: &note, CreatedAt: now.Add(time.Second)},
		{Id: model2ID, Iteration: &two, Role: "assistant", Mode: &task, Content: &empty, CreatedAt: now.Add(2 * time.Second), ModelCall: &conversationmodel.ModelCallView{MessageId: model2ID, Status: "running"}, ToolMessage: []*conversationmodel.ToolMessageView{{Id: "pending-report-message", Iteration: &two, ToolCall: &conversationmodel.ToolCallView{OpId: "pending-report-call", ToolName: "reporting/run_report", Status: "running"}}}},
	}
	for _, status := range []string{"running", "waiting_for_user"} {
		t.Run(status, func(t *testing.T) {
			state := BuildCanonicalState("conversation", convstore.Transcript{{Id: "native-turn", ConversationId: "conversation", Status: status, Message: messages}})
			turn := state.Turns[0]
			require.Len(t, turn.Execution.Pages, 2)
			require.Equal(t, model2ID, turn.Execution.Pages[1].PageID)
			require.Empty(t, turn.Execution.Pages[1].FinalAssistantMessageID, "unrelated public message must never become model2 final")
			require.False(t, turn.Execution.Pages[1].FinalResponse)
			require.Equal(t, "running", turn.Execution.Pages[1].ModelSteps[0].Status)
			require.Len(t, turn.Execution.Pages[1].ToolSteps, 1)
			require.Equal(t, "pending-report-call", turn.Execution.Pages[1].ToolSteps[0].ToolCallID)
			require.Equal(t, "running", turn.Execution.Pages[1].ToolSteps[0].Status)
			if turn.Assistant != nil {
				require.Nil(t, turn.Assistant.Final)
			}
			require.Contains(t, turn.Messages, &TurnMessageState{MessageID: noteID, Role: "assistant", Mode: task, Content: note, CreatedAt: now.Add(time.Second)})
			projected, _ := aguiBootstrapMessages(context.Background(), nil, &ConversationStateResponse{Conversation: state}, nil)
			found := false
			for _, m := range projected {
				if m["id"] == noteID {
					found = true
					metadata := m["metadata"].(map[string]any)["agently"].(map[string]any)["presentation"].(map[string]any)
					require.Equal(t, "standalone", metadata["messageKind"])
				}
			}
			require.True(t, found, "public note must be present in the normal bootstrap messages")
		})
	}
	messages = append(messages, &conversationmodel.MessageView{Id: "final-model", Iteration: &three, Role: "assistant", Mode: &task, Content: &final, CreatedAt: now.Add(3 * time.Second), ModelCall: &conversationmodel.ModelCallView{MessageId: "final-model", Status: "completed"}})
	completed := BuildCanonicalState("conversation", convstore.Transcript{{Id: "native-turn", ConversationId: "conversation", Status: "succeeded", Message: messages}}).Turns[0]
	require.Equal(t, "final-model", completed.Assistant.Final.MessageID)
	require.Empty(t, completed.Execution.Pages[1].FinalAssistantMessageID)
	require.Equal(t, "final-model", completed.Execution.Pages[2].FinalAssistantMessageID)
	require.Len(t, completed.Messages, 2)
}
