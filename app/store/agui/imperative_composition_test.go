package agui_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	store "github.com/viant/agently-core/app/store/agui"
	conversation "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/app/store/native"
	convservice "github.com/viant/agently-core/internal/service/conversation"
	authsvc "github.com/viant/agently-core/service/auth"
)

// The coordinator binds an existing native conversation, then composes
// conditional thread/run writers and readers inside one owned transaction.
// Buffered child defaults must not hide the prefix needed by later readers.
func TestNativeAGUIBoundThreadReadAfterWritePreservesRevisionAndRollback(t *testing.T) {
	for _, name := range []string{"AGENTLY_DB_DRIVER", "AGENTLY_DB_DSN", "AGENTLY_DB_PATH", "AGENTLY_DB_SECRETS"} {
		t.Setenv(name, "")
	}
	ctx := authsvc.InjectUser(context.Background(), "owner")
	server, err := native.New(ctx, native.Options{WorkspaceRoot: t.TempDir()})
	require.NoError(t, err)
	defer server.Shutdown(ctx)
	conv, err := convservice.New(ctx, server)
	require.NoError(t, err)
	original := conversation.NewConversation()
	original.SetId("existing-thread")
	original.SetCreatedByUserID("owner")
	original.SetVisibility("private")
	original.SetStatus("active")
	require.NoError(t, conv.PatchConversations(ctx, original))
	repository := store.New(server)
	admitted, created, err := repository.Admit(ctx, admission("existing-thread", "bound-run"))
	require.NoError(t, err)
	require.True(t, created)
	thread, err := repository.GetThread(ctx, "owner", "existing-thread")
	require.NoError(t, err)
	require.Equal(t, int64(1), thread.Revision)
	require.Equal(t, "existing-thread", admitted.ConversationID)
	appended, err := repository.Append(ctx, "owner", "existing-thread", "bound-run", admitted.Revision, []json.RawMessage{start("existing-thread", "bound-run")}, &store.Change{State: json.RawMessage(`{"stage":"accepted"}`), Messages: json.RawMessage(`[]`), ExpectedThreadRevision: thread.Revision})
	require.NoError(t, err)
	require.Equal(t, int64(2), appended.Revision)
	require.Equal(t, int64(1), appended.LastSequence)
	projection, err := repository.GetThread(ctx, "owner", "existing-thread")
	require.NoError(t, err)
	require.Equal(t, int64(2), projection.Revision)
	require.JSONEq(t, `{"stage":"accepted"}`, string(projection.State))
	// A stale conditional thread update must not release its event, state or run revision.
	_, err = repository.Append(ctx, "owner", "existing-thread", "bound-run", appended.Revision, []json.RawMessage{json.RawMessage(`{"type":"CUSTOM","name":"stale-attempt","value":null}`)}, &store.Change{State: json.RawMessage(`{"stage":"must-not-persist"}`), ExpectedThreadRevision: thread.Revision})
	require.ErrorIs(t, err, store.ErrConflict)
	after, err := repository.GetRun(ctx, "owner", "existing-thread", "bound-run")
	require.NoError(t, err)
	require.Equal(t, appended, after)
	held, err := repository.GetThread(ctx, "owner", "existing-thread")
	require.NoError(t, err)
	require.Equal(t, projection, held)
	events, err := repository.Replay(ctx, "owner", "existing-thread", "bound-run", 0, 100)
	require.NoError(t, err)
	require.Len(t, events, 1)
	_, err = repository.Append(ctx, "foreign", "existing-thread", "bound-run", appended.Revision, []json.RawMessage{json.RawMessage(`{"type":"CUSTOM","name":"foreign-attempt","value":null}`)}, nil)
	require.ErrorIs(t, err, store.ErrNotFound)
	events, err = repository.Replay(ctx, "owner", "existing-thread", "bound-run", 0, 100)
	require.NoError(t, err)
	require.Len(t, events, 1)
}
