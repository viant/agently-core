package conversation

import (
	"testing"

	"github.com/stretchr/testify/require"
	convcli "github.com/viant/agently-core/app/store/conversation"
)

func TestMessagePatchPayload(t *testing.T) {
	msg := convcli.NewMessage()
	msg.SetId("m1")
	msg.SetStatus("completed")
	msg.SetToolName("llm/agents-run")
	msg.SetInterim(0)
	msg.SetPreamble("delegating")
	msg.SetLinkedConversationID("child-123")

	got := messagePatchPayload(msg)
	require.EqualValues(t, "completed", got["status"])
	require.EqualValues(t, "llm/agents-run", got["toolName"])
	require.EqualValues(t, 0, got["interim"])
	require.EqualValues(t, "delegating", got["preamble"])
	require.EqualValues(t, "child-123", got["linkedConversationId"])
}

func TestMessagePatchPayload_Empty(t *testing.T) {
	msg := convcli.NewMessage()
	msg.SetId("m1")
	msg.SetConversationID("c1")
	msg.SetRole("assistant")
	msg.SetType("text")

	got := messagePatchPayload(msg)
	require.Len(t, got, 0)
}
