package sdk

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	aguistore "github.com/viant/agently-core/app/store/agui"
	"github.com/viant/agently-core/protocol/agui"
	"github.com/viant/agently-core/runtime/aguistate"
)

func TestAGUIApprovalReceiptProjectionIsIdempotentAfterJournalCommit(t *testing.T) {
	client := newRecoveryAGUIClient(t)
	ctx := recoveryContext()
	record, _, err := client.store.Admit(ctx, aguistore.Admission{ThreadID: "thread", RunID: "receipt-run", TurnID: "native-turn", Principal: "owner", Input: json.RawMessage(`{"threadId":"thread","runId":"receipt-run","messages":[]}`)})
	require.NoError(t, err)
	before := json.RawMessage(`[{"id":"assistant","role":"assistant","toolCalls":[{"id":"call","type":"function","function":{"name":"backend","arguments":"{\"n\":1}"}}]}]`)
	projection, err := aguistate.New(nil, before)
	require.NoError(t, err)
	translator := agui.NewTranslator(record.ThreadID, record.RunID)
	require.NoError(t, translator.SetNativeIdentity(record.TurnID))
	publicCallID := agui.ProtocolToolCallID(record.TurnID, "call")
	before = json.RawMessage(strings.ReplaceAll(string(before), `"id":"call"`, fmt.Sprintf(`"id":%q`, publicCallID)))
	var messages []json.RawMessage
	require.NoError(t, json.Unmarshal(before, &messages))
	require.NoError(t, translator.SeedMessages(messages))
	writer := &aguiJournalWriter{ctx: ctx, client: client, store: client.store, run: record, projection: projection}
	require.NoError(t, writer.write(encodeAGUIEvents(translator.Start()), nil))
	client.inspect = func(context.Context, *aguistore.Run) (*aguiRecoveredNative, error) {
		return &aguiRecoveredNative{Status: "waiting_for_user", Messages: []aguistate.Object{
			{"id": "assistant", "role": "assistant", "toolCalls": []any{map[string]any{"id": publicCallID, "type": "function", "function": map[string]any{"name": "backend", "arguments": "{\"n\":2}"}}}},
			{"id": "original-tool", "role": "tool", "toolCallId": publicCallID, "content": "original result"},
		}}, nil
	}
	callID := publicCallID
	interrupt := agui.WireInterrupt{ID: "approval", Reason: "approval", ToolCallID: &callID}
	require.NoError(t, aguiProjectApprovalReceipt(ctx, client, writer, translator, interrupt))
	journal, err := aguiRecoveryJournal(ctx, client.store, writer.run)
	require.NoError(t, err)
	restored, err := agui.RestoreTranslator(record.ThreadID, record.RunID, journal)
	require.NoError(t, err)
	sequence := writer.run.LastSequence
	require.NoError(t, aguiProjectApprovalReceipt(ctx, client, writer, restored, interrupt))
	require.Equal(t, sequence, writer.run.LastSequence, "receipt replay must not publish a second result or snapshot")
	count := 0
	for _, raw := range journal {
		var event struct {
			Type string `json:"type"`
		}
		require.NoError(t, json.Unmarshal(raw, &event))
		if event.Type == "TOOL_CALL_RESULT" {
			count++
		}
	}
	require.Equal(t, 1, count)
	require.Contains(t, string(rawAGUI(writer.projection.Messages)), `n\":2`)
}
