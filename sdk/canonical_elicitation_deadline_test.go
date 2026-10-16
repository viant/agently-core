package sdk

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	convstore "github.com/viant/agently-core/app/store/conversation"
	conversationmodel "github.com/viant/agently-core/model/conversation"
	"github.com/viant/agently-core/runtime/streaming"
)

func TestCanonicalTranscriptPreservesElicitationDeadline(t *testing.T) {
	const deadline = "2026-10-06T17:59:00Z"
	for _, enriched := range []bool{true, false} {
		name := "legacy_content"
		if enriched {
			name = "enriched_map"
		}
		t.Run(name, func(t *testing.T) {
			id, status := "ask", "pending"
			content := `{"message":"Choose","deadline":"` + deadline + `","requestedSchema":{"type":"object"}}`
			message := &conversationmodel.MessageView{Id: "ask-message", Role: "assistant", Type: "elicitation_request", ElicitationId: &id, Status: &status, Content: &content}
			if enriched {
				message.Elicitation = map[string]interface{}{"message": "Choose", "deadline": deadline, "requestedSchema": map[string]interface{}{"type": "object"}}
			}
			state := BuildCanonicalState("conversation", convstore.Transcript{{Id: "turn", ConversationId: "conversation", Status: "waiting_for_user", Message: []*conversationmodel.MessageView{message}}})
			require.Len(t, state.Turns, 1)
			require.NotNil(t, state.Turns[0].Elicitation)
			require.Equal(t, deadline, state.Turns[0].Elicitation.Deadline)
			raw, err := json.Marshal(state)
			require.NoError(t, err)
			require.Contains(t, string(raw), `"deadline":"`+deadline+`"`)
			require.Equal(t, deadline, pendingElicitation(state).Deadline)
		})
	}
}

func TestCanonicalElicitationEventPreservesDeadline(t *testing.T) {
	turn := &TurnState{}
	applyElicitationRequested(turn, &streaming.Event{ElicitationID: "ask", ElicitationData: map[string]interface{}{"deadline": "2026-10-06T17:59:00Z"}})
	require.NotNil(t, turn.Elicitation)
	require.Equal(t, "2026-10-06T17:59:00Z", turn.Elicitation.Deadline)
}
