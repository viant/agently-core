package sdk

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	aguistore "github.com/viant/agently-core/app/store/agui"
	"github.com/viant/agently-core/runtime/aguistate"
)

// This reproduces the previously confirmed Datly interleaving loss. It became
// mandatory after approval of per-message producer updates.
func TestAGUIConcurrentHistoryPreservesAcceptedUpdates(t *testing.T) {
	c := newRecoveryAGUIClient(t)
	ctx := recoveryContext()
	newWriter := func(run, turn, msg string) *aguiJournalWriter {
		record, _, err := c.store.Admit(ctx, aguistore.Admission{Principal: "owner", ThreadID: "thread", RunID: run, TurnID: turn, ClientMessageID: msg, Input: rawAGUI(map[string]any{"threadId": "thread", "runId": run, "messages": []any{map[string]any{"id": msg, "role": "user", "content": "hello"}}})})
		require.NoError(t, err)
		thread, err := c.store.GetThread(ctx, "owner", "thread")
		require.NoError(t, err)
		projection, err := aguistate.New(thread.State, thread.Messages)
		require.NoError(t, err)
		writer := &aguiJournalWriter{ctx: ctx, client: c, store: c.store, run: record, projection: projection}
		require.NoError(t, writer.write([]json.RawMessage{rawAGUI(map[string]any{"type": "RUN_STARTED", "threadId": "thread", "runId": run})}, nil))
		return writer
	}
	a := newWriter("a", "native-a", "user-a")
	require.NoError(t, a.write([]json.RawMessage{json.RawMessage(`{"type":"TEXT_MESSAGE_START","messageId":"a-answer","role":"assistant"}`), json.RawMessage(`{"type":"TEXT_MESSAGE_CONTENT","messageId":"a-answer","delta":"prefix"}`)}, nil))
	b := newWriter("b", "native-b", "user-b")
	require.NoError(t, b.write([]json.RawMessage{json.RawMessage(`{"type":"TEXT_MESSAGE_START","messageId":"b-answer","role":"assistant"}`), json.RawMessage(`{"type":"TEXT_MESSAGE_CONTENT","messageId":"b-answer","delta":"b"}`)}, nil))
	require.NoError(t, a.write([]json.RawMessage{json.RawMessage(`{"type":"TEXT_MESSAGE_CONTENT","messageId":"a-answer","delta":" suffix"}`)}, nil))
	require.NoError(t, b.write([]json.RawMessage{json.RawMessage(`{"type":"TEXT_MESSAGE_CONTENT","messageId":"b-answer","delta":" second"}`)}, nil))
	thread, err := c.store.GetThread(ctx, "owner", "thread")
	require.NoError(t, err)
	require.Contains(t, string(thread.Messages), "prefix suffix", "B must not revert A's already accepted suffix")
	require.Contains(t, string(thread.Messages), "b second")
}
