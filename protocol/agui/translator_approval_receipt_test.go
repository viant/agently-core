package agui

import (
	"encoding/json"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestNativeApprovalReceiptTrustSeamRetainsGenericProducerGuard(t *testing.T) {
	translator := NewTranslator("thread", "run")
	require.NoError(t, translator.SetNativeIdentity("native"))
	call := ProtocolToolCallID("native", "op")
	messages := rawJSON([]any{map[string]any{"id": "assistant", "role": "assistant", "toolCalls": []any{map[string]any{"id": call, "type": "function", "function": map[string]any{"name": "backend", "arguments": "{}"}}}}, map[string]any{"id": "presentation", "role": "activity", "activityType": "agently.turn", "content": map[string]any{"version": "1", "nativeTurnId": "native", "phase": "waiting"}}})
	var graph []json.RawMessage
	require.NoError(t, json.Unmarshal(messages, &graph))
	require.NoError(t, translator.SeedMessages(graph))
	snapshot, err := DecodeEvent(rawJSON(map[string]any{"type": "MESSAGES_SNAPSHOT", "messages": json.RawMessage(messages)}))
	require.NoError(t, err)
	result, err := DecodeEvent(rawJSON(map[string]any{"type": "TOOL_CALL_RESULT", "messageId": "original-tool", "toolCallId": call, "role": "tool", "content": "real result"}))
	require.NoError(t, err)
	_, err = translator.EmitStandard(snapshot)
	require.ErrorContains(t, err, "trusted producers")
	_, err = translator.ReconcileApprovalReceipt("foreign", call, snapshot, result)
	require.Error(t, err)
	events, err := translator.ReconcileApprovalReceipt("native", call, snapshot, result)
	require.NoError(t, err)
	require.Equal(t, "TOOL_CALL_RESULT", events[len(events)-1].Type)
	_, err = translator.ReconcileApprovalReceipt("native", ProtocolToolCallID("foreign", "op"), snapshot, result)
	require.Error(t, err)
}
