package sdk

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	convstore "github.com/viant/agently-core/app/store/conversation"
	conversationread "github.com/viant/agently-core/internal/datly/conversation/read"
	conversationmodel "github.com/viant/agently-core/model/conversation"
	"github.com/viant/agently-core/runtime/aguistate"
)

func TestCanonicalToolResponseDoesNotReplaceAssistantPage(t *testing.T) {
	task, phase, parent := "task", "sidecar", "assistant"
	contract := `{"name":"analytics_dashboard","format":"forge_report_data","instructions":"report-document-v1"}`
	// A legitimate assistant JSON response remains visible even when its body
	// happens to equal a tool response. Ownership, never body equality, decides.
	state := BuildCanonicalState("thread", convstore.Transcript{&convstore.Turn{Id: "turn", ConversationId: "thread", Message: []*conversationmodel.MessageView{
		{Id: parent, Role: "assistant", Mode: &task, Content: &contract, ModelCall: &conversationmodel.ModelCallView{MessageId: parent, Status: "completed"}},
		{Id: "tool-result", Role: "tool", Mode: &task, Phase: &phase, ParentMessageId: &parent, Content: &contract, MessageToolCall: &conversationread.MessageToolCallView{OpId: "op", ToolName: "template/get", Status: "completed"}},
	}}})
	page := state.Turns[0].Execution.Pages[0]
	require.Equal(t, parent, page.PageID)
	require.Equal(t, parent, page.AssistantMessageID)
	require.Equal(t, contract, page.Content)
	require.Len(t, page.ModelSteps, 1)
	require.Len(t, page.ToolSteps, 1)
	require.Equal(t, "tool-result", page.ToolSteps[0].ToolMessageID)
	require.Equal(t, contract, page.ToolSteps[0].Content)
	// Previously emitted tool-owned assistant prose is removed during replay.
	journal := []aguistate.Object{{"id": "tool-result", "role": "assistant", "content": contract}, {"id": "tool-result/activity", "role": "activity", "activityType": "agently.rendered-content", "content": map[string]any{"private": true}}}
	messages, _ := aguiBootstrapMessages(context.Background(), nil, &ConversationStateResponse{Conversation: state}, journal)
	for _, message := range messages {
		require.NotEqual(t, "tool-result", message["id"])
		require.NotEqual(t, "tool-result/activity", message["id"])
	}
	found := false
	for _, message := range messages {
		if message["id"] == parent {
			found = true
			require.Equal(t, contract, message["content"])
		}
	}
	require.True(t, found)
	// Genuine tool results retain their role and inspectable response unchanged.
	messages, _ = aguiBootstrapMessages(context.Background(), nil, &ConversationStateResponse{Conversation: state}, []aguistate.Object{{"id": "tool-result", "role": "tool", "toolCallId": "op", "content": contract}})
	require.Equal(t, "tool", messages[0]["role"])
	require.Equal(t, contract, messages[0]["content"])
}

func TestCanonicalStandaloneToolOnlyPageHasNoAssistantBody(t *testing.T) {
	contract := `{"format":"forge_report_data"}`
	state := BuildCanonicalState("thread", convstore.Transcript{&convstore.Turn{Id: "turn", ConversationId: "thread", Message: []*conversationmodel.MessageView{{Id: "tool-result", Role: "tool", Content: &contract, MessageToolCall: &conversationread.MessageToolCallView{OpId: "op", ToolName: "template/get", Status: "completed"}}}}})
	page := state.Turns[0].Execution.Pages[0]
	require.Empty(t, page.AssistantMessageID)
	require.Empty(t, page.Content)
	require.Empty(t, state.Turns[0].Assistant)
	require.Equal(t, contract, page.ToolSteps[0].Content)
}
