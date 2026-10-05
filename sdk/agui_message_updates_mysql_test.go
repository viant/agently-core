package sdk

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	aguistore "github.com/viant/agently-core/app/store/agui"
	"github.com/viant/agently-core/app/store/native"
	"github.com/viant/agently-core/runtime/aguistate"
	"github.com/viant/datly/bootstrap/connector"
)

func TestAGUIMySQLScopedMessageUpdatesPreserveConcurrentHistory(t *testing.T) {
	dsn := os.Getenv("AGENTLY_AGUI_MYSQL_DSN")
	if dsn == "" {
		t.Skip("AGENTLY_AGUI_MYSQL_DSN is not set")
	}
	ctx := recoveryContext()
	options := native.Options{Connectors: []connector.Config{{Name: "agently", Driver: "mysql", DSN: dsn}}}
	first, err := native.New(ctx, options)
	require.NoError(t, err)
	defer first.Shutdown(context.Background())
	second, err := native.New(ctx, options)
	require.NoError(t, err)
	defer second.Shutdown(context.Background())
	stores := []aguistore.Store{aguistore.New(first), aguistore.New(second)}
	threadID := uuid.NewString()
	newWriter := func(index int) *aguiJournalWriter {
		runID := uuid.NewString()
		record, _, err := stores[index].Admit(ctx, aguistore.Admission{Principal: "owner", ThreadID: threadID, RunID: runID, TurnID: uuid.NewString(), ClientMessageID: uuid.NewString(), Input: rawAGUI(map[string]any{"threadId": threadID, "runId": runID, "messages": []any{}})})
		require.NoError(t, err)
		thread, err := stores[index].GetThread(ctx, "owner", threadID)
		require.NoError(t, err)
		projection, err := aguistate.New(thread.State, thread.Messages)
		require.NoError(t, err)
		writer := &aguiJournalWriter{ctx: ctx, store: stores[index], run: record, projection: projection, messageBaseline: append(json.RawMessage(nil), thread.Messages...)}
		require.NoError(t, writer.write([]json.RawMessage{rawAGUI(map[string]any{"type": "RUN_STARTED", "threadId": threadID, "runId": runID})}, nil))
		return writer
	}
	a := newWriter(0)
	require.NoError(t, a.write([]json.RawMessage{json.RawMessage(`{"type":"TEXT_MESSAGE_START","messageId":"a","role":"assistant"}`), json.RawMessage(`{"type":"TEXT_MESSAGE_CONTENT","messageId":"a","delta":"prefix"}`)}, nil))
	b := newWriter(1)
	require.NoError(t, b.write([]json.RawMessage{json.RawMessage(`{"type":"TEXT_MESSAGE_START","messageId":"b","role":"assistant"}`), json.RawMessage(`{"type":"TEXT_MESSAGE_CONTENT","messageId":"b","delta":"b"}`)}, nil))
	require.NoError(t, a.write([]json.RawMessage{json.RawMessage(`{"type":"TEXT_MESSAGE_CONTENT","messageId":"a","delta":" suffix"}`)}, nil))
	require.NoError(t, b.write([]json.RawMessage{json.RawMessage(`{"type":"TEXT_MESSAGE_CONTENT","messageId":"b","delta":" second"}`)}, nil))
	require.NoError(t, b.write([]json.RawMessage{rawAGUI(map[string]any{"type": "MESSAGES_SNAPSHOT", "messages": b.projection.Messages})}, nil))
	thread, err := stores[0].GetThread(ctx, "owner", threadID)
	require.NoError(t, err)
	require.Contains(t, string(thread.Messages), "prefix suffix")
	require.Contains(t, string(thread.Messages), "b second")
	events, err := stores[0].Replay(ctx, "owner", threadID, b.run.RunID, 0, 256)
	require.NoError(t, err)
	require.Contains(t, string(events[len(events)-1].Event), "prefix suffix", "snapshot must publish the canonical merged history")
}
