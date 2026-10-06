package sdk

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	aguistore "github.com/viant/agently-core/app/store/agui"
	"github.com/viant/agently-core/app/store/native"
	"github.com/viant/agently-core/runtime/aguistate"
)

func messageUpdateFixture(t *testing.T) (aguistore.Store, string, func(string) *aguiJournalWriter) {
	t.Helper()
	ctx := recoveryContext()
	workspace := t.TempDir()
	server, err := native.New(ctx, native.Options{WorkspaceRoot: workspace})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Shutdown(ctx)) })
	store := aguistore.New(server)
	newWriter := func(run string) *aguiJournalWriter {
		record, _, err := store.Admit(ctx, aguistore.Admission{Principal: "owner", ThreadID: "thread", RunID: run, TurnID: "native-" + run, ClientMessageID: "user-" + run, Input: rawAGUI(map[string]any{"threadId": "thread", "runId": run, "messages": []any{map[string]any{"id": "user-" + run, "role": "user", "content": "hello"}}})})
		require.NoError(t, err)
		thread, err := store.GetThread(ctx, "owner", "thread")
		require.NoError(t, err)
		projection, err := aguistate.New(thread.State, thread.Messages)
		require.NoError(t, err)
		writer := &aguiJournalWriter{ctx: ctx, store: store, run: record, projection: projection, messageBaseline: append(json.RawMessage(nil), thread.Messages...)}
		require.NoError(t, writer.write([]json.RawMessage{rawAGUI(map[string]any{"type": "RUN_STARTED", "threadId": "thread", "runId": run}), rawAGUI(map[string]any{"type": "MESSAGES_SNAPSHOT", "messages": projection.Messages})}, nil))
		return writer
	}
	return store, workspace, newWriter
}
func TestAGUIConcurrentSameMessageConflictRollsBackAndKeepsReplay(t *testing.T) {
	store, _, newWriter := messageUpdateFixture(t)
	a := newWriter("a")
	require.NoError(t, a.write([]json.RawMessage{json.RawMessage(`{"type":"TEXT_MESSAGE_START","messageId":"shared","role":"assistant"}`), json.RawMessage(`{"type":"TEXT_MESSAGE_CONTENT","messageId":"shared","delta":"prefix"}`)}, nil))
	b := newWriter("b")
	require.NoError(t, b.write([]json.RawMessage{json.RawMessage(`{"type":"TEXT_MESSAGE_START","messageId":"shared","role":"assistant"}`)}, nil))
	require.NoError(t, a.write([]json.RawMessage{json.RawMessage(`{"type":"TEXT_MESSAGE_CONTENT","messageId":"shared","delta":" accepted"}`)}, nil))
	baseline := append([]byte(nil), b.messageBaseline...)
	local, _ := json.Marshal(b.projection)
	revision, sequence := b.run.Revision, b.run.LastSequence
	require.ErrorIs(t, b.write([]json.RawMessage{json.RawMessage(`{"type":"TEXT_MESSAGE_CONTENT","messageId":"shared","delta":" rejected"}`)}, nil), aguistore.ErrConflict)
	afterLocal, _ := json.Marshal(b.projection)
	require.Equal(t, string(local), string(afterLocal))
	require.Equal(t, string(baseline), string(b.messageBaseline))
	require.Equal(t, revision, b.run.Revision)
	require.Equal(t, sequence, b.run.LastSequence)
	thread, err := store.GetThread(context.Background(), "owner", "thread")
	require.NoError(t, err)
	require.Contains(t, string(thread.Messages), "prefix accepted")
	require.NotContains(t, string(thread.Messages), "rejected")
	replay, err := store.Replay(context.Background(), "owner", "thread", "b", 0, 100)
	require.NoError(t, err)
	require.Len(t, replay, int(sequence))
	for _, event := range replay {
		require.NotContains(t, string(event.Event), "rejected")
	}
	// Recovery begins from its own accepted journal and the current canonical
	// thread, rather than rebasing and silently accepting the conflicting edit.
	run, err := store.GetRun(recoveryContext(), "owner", "thread", "b")
	require.NoError(t, err)
	journal, err := aguiRecoveryJournal(recoveryContext(), store, run)
	require.NoError(t, err)
	recovered, err := aguiRecoveryProjection(thread, journal)
	require.NoError(t, err)
	require.NotContains(t, string(rawAGUI(recovered.Messages)), "rejected")
}
func TestAGUIMessageDiffCanonicalSnapshotAndLateDatlyFailureAreAtomic(t *testing.T) {
	store, workspace, newWriter := messageUpdateFixture(t)
	a := newWriter("a")
	require.NoError(t, a.write([]json.RawMessage{json.RawMessage(`{"type":"TEXT_MESSAGE_START","messageId":"a-answer","role":"assistant"}`), json.RawMessage(`{"type":"TEXT_MESSAGE_CONTENT","messageId":"a-answer","delta":"prefix"}`)}, nil))
	b := newWriter("b")
	require.NoError(t, b.write([]json.RawMessage{json.RawMessage(`{"type":"TEXT_MESSAGE_START","messageId":"b-answer","role":"assistant"}`), json.RawMessage(`{"type":"TEXT_MESSAGE_CONTENT","messageId":"b-answer","delta":"b"}`)}, nil))
	require.NoError(t, a.write([]json.RawMessage{json.RawMessage(`{"type":"TEXT_MESSAGE_CONTENT","messageId":"a-answer","delta":" suffix"}`)}, nil))
	// b's local history deliberately still has A's old prefix. The outgoing
	// authoritative snapshot must include A's latest accepted content.
	snapshot := rawAGUI(map[string]any{"type": "MESSAGES_SNAPSHOT", "messages": b.projection.Messages})
	require.NoError(t, b.write([]json.RawMessage{snapshot}, nil))
	replay, err := store.Replay(recoveryContext(), "owner", "thread", "b", 0, 100)
	require.NoError(t, err)
	require.Contains(t, string(replay[len(replay)-1].Event), "prefix suffix")
	require.NotContains(t, string(rawAGUI(b.projection.Messages)), "prefix suffix", "producer-local history must not acquire another producer's changes as its own")
	thread, err := store.GetThread(recoveryContext(), "owner", "thread")
	require.NoError(t, err)
	baseline := append([]byte(nil), b.messageBaseline...)
	local, _ := json.Marshal(b.projection)
	revision, sequence := b.run.Revision, b.run.LastSequence
	// Fixture-only DDL faults the final journal insert after Datly has written
	// the earlier thread/run nodes. All domain persistence remains Datly 1.0.
	db, err := sql.Open("sqlite3", filepath.Join(workspace, "db", "agently-core.db"))
	require.NoError(t, err)
	defer db.Close()
	_, err = db.Exec(`CREATE TRIGGER reject_message_diff BEFORE INSERT ON call_payload WHEN NEW.kind='agui.event' AND CAST(NEW.inline_body AS TEXT) LIKE '%"name":"reject-message-diff"%' BEGIN SELECT RAISE(ABORT,'message-diff-fixture-reject'); END`)
	require.NoError(t, err)
	require.Error(t, b.write([]json.RawMessage{json.RawMessage(`{"type":"TEXT_MESSAGE_CONTENT","messageId":"b-answer","delta":" rejected"}`), json.RawMessage(`{"type":"CUSTOM","name":"reject-message-diff","value":null}`)}, nil))
	latest, err := store.GetThread(recoveryContext(), "owner", "thread")
	require.NoError(t, err)
	require.Equal(t, string(thread.Messages), string(latest.Messages))
	require.Equal(t, thread.Revision, latest.Revision)
	afterLocal, _ := json.Marshal(b.projection)
	require.Equal(t, string(local), string(afterLocal))
	require.Equal(t, string(baseline), string(b.messageBaseline))
	require.Equal(t, revision, b.run.Revision)
	require.Equal(t, sequence, b.run.LastSequence)
	run, err := store.GetRun(recoveryContext(), "owner", "thread", "b")
	require.NoError(t, err)
	require.Equal(t, sequence, run.LastSequence)
	require.Equal(t, revision, run.Revision)
	replay, err = store.Replay(recoveryContext(), "owner", "thread", "b", sequence, 100)
	require.NoError(t, err)
	require.Empty(t, replay)
}
