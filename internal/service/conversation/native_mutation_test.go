package conversation

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"
	convcli "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/runtime/streaming"
)

type mutationEventRecorder struct{ events []*streaming.Event }

func (r *mutationEventRecorder) Publish(_ context.Context, event *streaming.Event) error {
	r.events = append(r.events, event)
	return nil
}

func TestService_NativeMessageMutationPreservesCallerFieldsAndFailure(t *testing.T) {
	svc := newQueueService(t, func(t *testing.T, db *sql.DB) {
		_, err := db.Exec(`CREATE TRIGGER reject_message BEFORE INSERT ON message WHEN NEW.id = 'failed' BEGIN SELECT RAISE(ABORT, 'mutation rejected'); END`)
		require.NoError(t, err)
	})
	ctx := context.Background()
	conversation := convcli.NewConversation()
	conversation.SetId("mutable-conversation")
	require.NoError(t, svc.PatchConversations(ctx, conversation))
	require.NotNil(t, conversation.CreatedAt)
	require.True(t, conversation.Has.CreatedAt)
	turn := convcli.NewTurn()
	turn.SetId("mutable-turn")
	turn.SetConversationID(conversation.Id)
	turn.SetStatus("running")
	require.NoError(t, svc.PatchTurn(ctx, turn))
	require.NotNil(t, turn.CreatedAt)
	require.True(t, turn.Has.CreatedAt)
	message := convcli.NewMessage()
	message.SetId("mutable-message")
	message.SetConversationID(conversation.Id)
	message.SetTurnID(turn.Id)
	message.SetRole("user")
	message.SetType("text")
	message.SetContent("hello")
	original := message
	require.NoError(t, svc.PatchMessage(ctx, message))
	require.Same(t, original, message)
	require.NotNil(t, message.CreatedAt)
	require.NotNil(t, message.Interim)
	require.NotNil(t, message.Sequence)
	require.True(t, message.Has.CreatedAt)
	require.True(t, message.Has.Interim)
	require.True(t, message.Has.Sequence)

	sparse := convcli.NewMessage()
	sparse.SetId(message.Id)
	sparse.SetStatus("succeeded")
	require.NoError(t, svc.PatchMessage(ctx, sparse))
	require.Equal(t, conversation.Id, sparse.ConversationID)
	require.Equal(t, turn.Id, *sparse.TurnID)
	require.Equal(t, "user", sparse.Role)
	require.Equal(t, "text", sparse.Type)
	require.True(t, sparse.Has.ConversationID)
	require.True(t, sparse.Has.TurnID)
	require.True(t, sparse.Has.Role)
	require.True(t, sparse.Has.Type)
	require.True(t, sparse.Has.UpdatedAt)
	require.NotNil(t, sparse.UpdatedAt)
	require.False(t, sparse.Has.Sequence)
	require.Nil(t, sparse.Sequence)
	require.False(t, sparse.Has.CreatedAt)
	require.Nil(t, sparse.CreatedAt)

	recorder := &mutationEventRecorder{}
	svc.SetStreamPublisher(recorder)
	failed := convcli.NewMessage()
	failed.SetId("failed")
	failed.SetConversationID(conversation.Id)
	failed.SetTurnID(turn.Id)
	failed.SetRole("assistant")
	failed.SetType("text")
	failed.SetContent("failure")
	require.ErrorContains(t, svc.PatchMessage(requestctx.WithMessageAddEvent(ctx), failed), "mutation rejected")
	require.Nil(t, failed.CreatedAt)
	require.Nil(t, failed.Sequence)
	require.Nil(t, failed.Interim)
	require.False(t, failed.Has.CreatedAt)
	require.False(t, failed.Has.Sequence)
	require.False(t, failed.Has.Interim)
	require.Empty(t, recorder.events)
	got, err := svc.GetMessage(ctx, failed.Id)
	require.NoError(t, err)
	require.Nil(t, got)
}
