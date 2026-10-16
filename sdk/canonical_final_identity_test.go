package sdk

import (
	"context"
	"github.com/stretchr/testify/require"
	convstore "github.com/viant/agently-core/app/store/conversation"
	conversationmodel "github.com/viant/agently-core/model/conversation"
	"testing"
	"time"
)

// These are stored producer records, not fabricated public assistant output.
// The execution-page path must retain the real final identity and timestamp.
func TestCanonicalFinalRetainsStoredTaskAndLegacyTimestamp(t *testing.T) {
	for _, mode := range []string{"task", ""} {
		t.Run("mode="+mode, func(t *testing.T) {
			now := time.Date(2026, 10, 9, 17, 37, 6, 0, time.UTC)
			task, exec := "task", "exec"
			empty, narration, final := "", "Linked operation completed", "Reviewed final from actual stored task"
			selectedMode := &mode
			if mode == "" {
				selectedMode = nil
			}
			messages := []*conversationmodel.MessageView{{Id: "parent", Role: "assistant", Type: "text", Mode: &task, Content: &empty, CreatedAt: now, ModelCall: &conversationmodel.ModelCallView{MessageId: "parent", Status: "completed"}}, {Id: "native-final", Role: "assistant", Type: "text", Mode: selectedMode, Content: &final, CreatedAt: now.Add(4 * time.Second), ModelCall: &conversationmodel.ModelCallView{MessageId: "native-final", Status: "completed"}}, {Id: "exec-narration", Role: "assistant", Type: "text", Mode: &exec, Content: &narration, CreatedAt: now.Add(5 * time.Second)}}
			toolContent := `{"status":"completed","linkedConversationId":"owned-child"}`
			tool := &conversationmodel.MessageView{Id: "linked-tool", Role: "tool", Type: "text", Mode: &task, Content: &toolContent, CreatedAt: now.Add(3 * time.Second)}
			// A later user/control carrier is neither a new final nor a replacement
			// for the stored final's ordering relative to the completed linked tool.
			carrierContent := "stored attachment metadata"
			messages = append(messages, tool, &conversationmodel.MessageView{Id: "late-carrier", Role: "user", Type: "control", Content: &carrierContent, CreatedAt: now.Add(6 * time.Second)})
			actual := BuildCanonicalState("conversation", convstore.Transcript{{Id: "turn", ConversationId: "conversation", Status: "succeeded", Message: messages}})
			require.NotNil(t, actual.Turns[0].Assistant)
			require.NotNil(t, actual.Turns[0].Assistant.Final)
			require.Equal(t, "native-final", actual.Turns[0].Assistant.Final.MessageID)
			require.Equal(t, final, actual.Turns[0].Assistant.Final.Content)
			require.Equal(t, now.Add(4*time.Second), actual.Turns[0].Assistant.Final.CreatedAt)
			require.True(t, actual.Turns[0].Assistant.Final.CreatedAt.After(tool.CreatedAt))
			omitCanonicalModelPayloads(actual)
			require.Equal(t, now.Add(4*time.Second), actual.Turns[0].Assistant.Final.CreatedAt, "compact model payloads retain stored chronology")
		})
	}
}

func TestAGUIBootstrapRetainsRawLegacyAssistantWithoutModeOrModelCall(t *testing.T) {
	content := "Actual stored legacy assistant final"
	now := time.Date(2026, 10, 9, 17, 0, 0, 0, time.UTC)
	transcript := convstore.Transcript{{Id: "legacy-turn", ConversationId: "legacy-conversation", Status: "succeeded", Message: []*conversationmodel.MessageView{{Id: "legacy-assistant", Role: "assistant", Type: "text", Content: &content, CreatedAt: now}}}}
	state := BuildCanonicalState("legacy-conversation", transcript)
	require.Equal(t, content, state.Turns[0].Assistant.Final.Content)
	messages, _ := aguiBootstrapMessages(context.Background(), nil, &ConversationStateResponse{Conversation: state}, nil)
	found := false
	for _, message := range messages {
		if message["id"] == "legacy-assistant" {
			require.Equal(t, "assistant", message["role"])
			require.Equal(t, content, message["content"])
			found = true
		}
	}
	require.True(t, found, "actual legacy final must survive canonical -> AGUI bootstrap")
}
