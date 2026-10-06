package sdk

import (
	"context"
	"github.com/stretchr/testify/require"
	convstore "github.com/viant/agently-core/app/store/conversation"
	convread "github.com/viant/agently-core/internal/datly/conversation/read"
	"github.com/viant/agently-core/runtime/aguistate"
	"testing"
)

func TestCanonicalHostOriginUsesOnlyExactServerMarker(t *testing.T) {
	start := "guest"
	for _, test := range []struct {
		name           string
		id, role, kind string
		interim        int
		match          bool
	}{
		{"exact", "guest", "assistant", "host_request", 1, true}, {"other-id", "other", "assistant", "host_request", 1, false}, {"other-role", "guest", "user", "host_request", 1, false}, {"other-type", "guest", "assistant", "text", 1, false}, {"not-interim", "guest", "assistant", "host_request", 0, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			turn := &convstore.Turn{Id: "guest", StartedByMessageId: &start, Message: []*convread.MessageView{{Id: test.id, Role: test.role, Type: test.kind, Interim: test.interim}}}
			state := buildTurnState(turn)
			if test.match {
				require.Equal(t, "host_request", state.Origin)
			} else {
				require.Empty(t, state.Origin)
			}
		})
	}
}
func TestAGUIBootstrapExcludesHostTurnsFromStandardMessagesWithoutChangingTranscript(t *testing.T) {
	transcript := &ConversationStateResponse{Conversation: &ConversationState{ConversationID: "thread", Turns: []*TurnState{
		{TurnID: "normal", Origin: "interactive", Messages: []*TurnMessageState{{MessageID: "normal-assistant", Role: "assistant", Content: "ordinary chat"}}},
		{TurnID: "guest", Origin: "host_request", StartedByMessageID: "host-parent", Messages: []*TurnMessageState{{MessageID: "host-parent", Role: "assistant", Content: "host marker"}, {MessageID: "host-tool", Role: "tool", Content: "host-only reply"}}, Assistant: &AssistantState{Final: &AssistantMessageState{MessageID: "host-final", Content: "host-only reply"}}},
	}}}
	before := string(rawAGUI(transcript))
	journal := []aguistate.Object{{"id": "normal-assistant", "role": "assistant", "content": "ordinary chat"}, {"id": "host-tool", "role": "tool", "toolCallId": "host-op", "content": "host-only reply"}, {"id": "host-lifecycle", "role": "activity", "activityType": "agently.turn", "content": map[string]any{"nativeTurnId": "guest", "phase": "completed"}}, {"id": "host-extra", "role": "assistant", "content": "another host reply", "metadata": map[string]any{"agently": map[string]any{"presentation": map[string]any{"version": "1", "nativeTurnId": "guest"}}}}}
	messages, quality := aguiBootstrapMessages(context.Background(), nil, transcript, journal)
	require.Len(t, messages, 1)
	require.Equal(t, "normal-assistant", messages[0]["id"])
	require.False(t, quality.Lossless)
	require.Contains(t, quality.UnavailableMessageIDs, "host-tool")
	require.NotContains(t, string(rawAGUI(messages)), "host-only reply")
	require.Equal(t, before, string(rawAGUI(transcript)))
}
