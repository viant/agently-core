package agui_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	store "github.com/viant/agently-core/app/store/agui"
	"github.com/viant/agently-core/app/store/data"
	"github.com/viant/agently-core/app/store/native"
	conversationmodel "github.com/viant/agently-core/model/conversation"
)

func TestOpaqueThreadMappingAndPromotion(t *testing.T) {
	ctx := context.Background()
	server, err := native.New(ctx, native.Options{WorkspaceRoot: t.TempDir()})
	require.NoError(t, err)
	defer server.Shutdown(ctx)
	repository := store.New(server)
	ids := []string{"Opaque", "opaque", "Opaque ", " Opaque", "opaque/日本語"}
	seen := map[string]bool{}
	for _, wire := range ids {
		admitted, created, err := repository.Admit(ctx, admission(wire, "public run "))
		require.NoError(t, err)
		require.True(t, created)
		require.Equal(t, wire, admitted.ThreadID)
		require.Equal(t, "public run ", admitted.RunID)
		require.NotEqual(t, wire, admitted.ConversationID)
		_, err = uuid.Parse(admitted.ConversationID)
		require.NoError(t, err)
		require.False(t, seen[admitted.ConversationID])
		seen[admitted.ConversationID] = true
		thread, err := repository.GetThread(ctx, "owner", wire)
		require.NoError(t, err)
		require.True(t, thread.ProtocolOnly)
		reverse, err := repository.GetThreadByConversationID(ctx, "owner", thread.ConversationID)
		require.NoError(t, err)
		require.Equal(t, thread, reverse)
		promoted, err := repository.PromoteThread(ctx, "owner", wire)
		require.NoError(t, err)
		require.False(t, promoted.ProtocolOnly)
		require.Equal(t, thread.ConversationID, promoted.ConversationID)
		require.Equal(t, wire, promoted.ThreadID)
		_, err = repository.GetThreadByConversationID(ctx, "foreign", thread.ConversationID)
		require.ErrorIs(t, err, store.ErrNotFound)
	}
}

func TestNativeConversationExactBindingAndForeignRejection(t *testing.T) {
	ctx := context.Background()
	server, err := native.New(ctx, native.Options{WorkspaceRoot: t.TempDir()})
	require.NoError(t, err)
	defer server.Shutdown(ctx)
	service := data.NewService(server)
	repository := store.New(server)
	for _, owner := range []string{"owner", "foreign"} {
		id := "native-non-uuid-" + owner
		row := conversationmodel.NewMutableConversationView(conversationmodel.WithConversationID(id), conversationmodel.WithConversationStatus("active"))
		row.SetCreatedByUserID(owner)
		_, err = service.PatchConversations(ctx, []*conversationmodel.MutableConversationView{row})
		require.NoError(t, err)
		admitted, _, err := repository.Admit(ctx, admission(id, "public"))
		if owner == "foreign" {
			require.ErrorIs(t, err, store.ErrNotFound)
			continue
		}
		require.NoError(t, err)
		require.Equal(t, id, admitted.ConversationID)
		thread, err := repository.GetThread(ctx, owner, id)
		require.NoError(t, err)
		require.False(t, thread.ProtocolOnly)
		require.Equal(t, id, thread.ThreadID)
	}
}

func TestIdentityKeysFrameExactBytes(t *testing.T) {
	require.NotEqual(t, store.RunKey("ab", "c", "d"), store.RunKey("a", "bc", "d"))
	require.NotEqual(t, store.ThreadKey("x"), store.ThreadKey("x "))
	require.NotEqual(t, store.ThreadKey("X"), store.ThreadKey("x"))
	require.NotEqual(t, store.SourceKey("a", "b", "c"), store.InitialTurnKey("a", "b", "c"))
}
