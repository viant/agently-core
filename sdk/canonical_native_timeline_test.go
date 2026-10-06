package sdk

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	convstore "github.com/viant/agently-core/app/store/conversation"
	conversationmodel "github.com/viant/agently-core/model/conversation"
)

// The same canonicalizer-produced fixture is decoded by both native SDKs.
func TestCanonicalModelInclusiveNativeTimelineFixture(t *testing.T) {
	modeRouter, modeTask := "router", "task"
	user, router, interim, narration, final := "Summarize results", `{"classification":true}`, "Preliminary findings", "Checking results", "Final report"
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	messages := []*conversationmodel.MessageView{
		{Id: "user", Role: "user", Content: &user, CreatedAt: now},
		{Id: "router", Role: "assistant", Mode: &modeRouter, Content: &router, CreatedAt: now.Add(time.Second), ModelCall: &conversationmodel.ModelCallView{MessageId: "router", Status: "completed"}},
		{Id: "interim", Role: "assistant", Mode: &modeTask, Content: &interim, CreatedAt: now.Add(2 * time.Second), ModelCall: &conversationmodel.ModelCallView{MessageId: "interim", Status: "completed"}},
		{Id: "narration", Role: "assistant", Content: &narration, Narration: &narration, Interim: 1, CreatedAt: now.Add(3 * time.Second)},
		{Id: "final", Role: "assistant", Mode: &modeTask, Content: &final, CreatedAt: now.Add(4 * time.Second), ModelCall: &conversationmodel.ModelCallView{MessageId: "final", Status: "completed"}},
	}
	state := BuildCanonicalState("thread", convstore.Transcript{&convstore.Turn{Id: "turn", ConversationId: "thread", Status: "completed", Message: messages}})
	require.Len(t, state.Turns[0].Messages, 3)
	data, err := json.MarshalIndent(state.Turns[0], "", "  ")
	require.NoError(t, err)
	for _, path := range []string{"android/src/test/resources/canonical-native-timeline.json", "ios/Tests/AgentlySDKTests/Fixtures/canonical-native-timeline.json"} {
		if os.Getenv("UPDATE_CANONICAL_TIMELINE_FIXTURE") == "1" {
			require.NoError(t, os.WriteFile(path, data, 0644))
		}
		expected, err := os.ReadFile(path)
		require.NoError(t, err)
		require.JSONEq(t, string(expected), string(data))
	}
}
