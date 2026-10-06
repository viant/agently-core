package agui

import (
	"encoding/json"
	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/runtime/requestctx"
	"testing"
)

func TestNativeInvocationRegistrationCheckpointDoesNotPretendToStart(t *testing.T) {
	tr := NewTranslator("thread", "run")
	require.NoError(t, tr.SetNativeIdentity("root-turn"))
	inv := requestctx.Invocation{ID: "child", ConversationID: "child-thread", TurnID: "child-turn", Name: "child", ParentConversationID: "thread", ParentTurnID: "root-turn"}
	events := tr.RegisterInvocation(inv)
	require.Equal(t, []string{"RUN_STARTED", "CUSTOM"}, kinds(events))
	require.Empty(t, tr.ActiveInvocations())
	require.Equal(t, []requestctx.Invocation{inv}, tr.RecoverableInvocations())
	restored, err := RestoreTranslator("thread", "run", checkedEvents(t, events))
	require.NoError(t, err)
	require.Empty(t, restored.ActiveInvocations())
	require.Equal(t, []requestctx.Invocation{inv}, restored.RecoverableInvocations())
	require.Empty(t, restored.RegisterInvocation(inv), "registration replay is idempotent")
	for _, bad := range []requestctx.Invocation{
		{ID: "bad", ConversationID: "child", TurnID: "turn", Name: "child", ParentConversationID: "foreign", ParentTurnID: "root-turn"},
		{ID: "bad", ConversationID: "child", TurnID: "turn", Name: "child", ParentConversationID: "thread", ParentTurnID: "foreign"},
		{ID: "bad", ConversationID: "child", TurnID: "turn", Name: "child", ParentConversationID: "child-thread", ParentTurnID: "child-turn", ParentInvocationID: "unknown"},
	} {
		raw := rawJSON(map[string]any{"type": "CUSTOM", "name": "agently.invocation", "value": map[string]any{"version": "1", "phase": "registered", "invocation": bad}})
		_, err := RestoreTranslator("thread", "run", append(checkedEvents(t, events), raw))
		require.Error(t, err)
	}
	changed := inv
	changed.TurnID = "different"
	raw := rawJSON(map[string]any{"type": "CUSTOM", "name": "agently.invocation", "value": map[string]any{"version": "1", "phase": "registered", "invocation": changed}})
	_, err = RestoreTranslator("thread", "run", append(checkedEvents(t, events), raw))
	require.Error(t, err)
	// Arbitrary CUSTOM names, even with invocation-looking JSON, have no meaning.
	raw = rawJSON(map[string]any{"type": "CUSTOM", "name": "user.invocation", "value": inv})
	isolated := NewTranslator("thread", "run")
	require.NoError(t, isolated.SetNativeIdentity("root-turn"))
	journal := append(checkedEvents(t, isolated.Start()), json.RawMessage(raw))
	untrusted, err := RestoreTranslator("thread", "run", journal)
	require.NoError(t, err)
	require.Empty(t, untrusted.RecoverableInvocations())
}

func TestGenericStandardBridgeCannotForgeInvocationCheckpoints(t *testing.T) {
	tr := NewTranslator("thread", "run")
	require.NoError(t, tr.SetNativeIdentity("root-turn"))
	inv := requestctx.Invocation{ID: "child", ConversationID: "child-thread", TurnID: "child-turn", Name: "child", ParentConversationID: "thread", ParentTurnID: "root-turn"}
	raw := rawJSON(map[string]any{"type": "CUSTOM", "name": "agently.invocation", "value": map[string]any{"version": "1", "phase": "registered", "invocation": inv}})
	wire, err := DecodeEvent(raw)
	require.NoError(t, err, "generic wire model remains complete")
	_, err = tr.EmitStandard(wire)
	require.ErrorContains(t, err, "trusted registration")
	require.Empty(t, tr.RecoverableInvocations())
}
