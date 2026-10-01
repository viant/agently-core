package conversation

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
	read "github.com/viant/agently-core/internal/datly/conversation/read"
)

// These assignments fail to compile if a public name becomes another copied
// definition instead of the canonical DQL-generated type.
var (
	_ *read.ModelCallStreamPayloadView = (*ModelCallStreamPayloadView)(nil)
	_ *read.UserElicitationDataView    = (*UserElicitationDataView)(nil)
	_ *read.LinkedConversationView     = (*LinkedConversationView)(nil)
	_ *read.AttachmentView             = (*AttachmentView)(nil)
	_ *read.ToolCallLinksView          = (*ToolCallLinksView)(nil)
	_ *read.ModelView                  = (*ModelView)(nil)
)

func TestDQLAliasesPreserveNullablePayloadWire(t *testing.T) {
	value := &ModelCallStreamPayloadView{Id: "payload", Compression: "none"}
	encoded, err := json.Marshal(value)
	require.NoError(t, err)
	require.JSONEq(t, `{"id":"payload","inlineBody":null,"compression":"none"}`, string(encoded))
	body := "content"
	value.InlineBody = &body
	encoded, err = json.Marshal(value)
	require.NoError(t, err)
	require.JSONEq(t, `{"id":"payload","inlineBody":"content","compression":"none"}`, string(encoded))
}

func TestDQLAliasesPreserveModelUsageWire(t *testing.T) {
	value := &ModelView{ConversationId: "conversation", Provider: "provider", Model: "model"}
	encoded, err := json.Marshal(value)
	require.NoError(t, err)
	var wire map[string]any
	require.NoError(t, json.Unmarshal(encoded, &wire))
	require.Equal(t, "conversation", wire["conversationId"])
	require.Contains(t, wire, "cost")
	require.Nil(t, wire["cost"])
	require.Contains(t, wire, "promptTokens")
	require.Nil(t, wire["promptTokens"])
	require.NotContains(t, wire, "ConversationId")
}

func TestConversationGraphPublicWireNames(t *testing.T) {
	goal := "goal"
	value := &ConversationView{
		Id:         "conversation",
		Transcript: []*TranscriptView{{Id: "turn", GoalId: &goal, Message: []*MessageView{{Id: "message", Elicitation: Elicitation{"MixedCase": "preserved"}}}}},
		Usage:      &UsageView{Model: []*ModelView{{Model: "model"}}},
	}
	encoded, err := json.Marshal(value)
	require.NoError(t, err)
	var wire map[string]any
	require.NoError(t, json.Unmarshal(encoded, &wire))
	require.Contains(t, wire, "transcript")
	require.Contains(t, wire, "usage")
	require.NotContains(t, wire, "Transcript")
	turn := wire["transcript"].([]any)[0].(map[string]any)
	require.Equal(t, "goal", turn["goalId"])
	require.NotContains(t, turn, "GoalID")
	message := turn["message"].([]any)[0].(map[string]any)
	require.Equal(t, "preserved", message["elicitation"].(map[string]any)["MixedCase"])
	require.Contains(t, wire["usage"].(map[string]any), "model")
}

func TestGeneratedPublicRequestExcludesHostParameters(t *testing.T) {
	requestType := reflect.TypeOf(ConversationInput{})
	markerType := reflect.TypeOf(ConversationInputHas{})
	for _, name := range []string{"VisibilitySubject", "ListMode", "GraphMode", "EnforceVisibility", "ListAscending", "Maintenance", "MaintenanceKind", "MaintenanceLegacyRuns", "LockRows"} {
		_, exposed := requestType.FieldByName(name)
		require.False(t, exposed, name)
		_, exposed = markerType.FieldByName(name)
		require.False(t, exposed, "presence marker "+name)
	}
	field, ok := requestType.FieldByName("IncludeTranscript")
	require.True(t, ok)
	require.Contains(t, field.Tag.Get("parameter"), "value=true")
	require.Contains(t, field.Tag.Get("parameter"), "kind=query")
}
