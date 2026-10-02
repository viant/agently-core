package native_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	authctx "github.com/viant/agently-core/internal/auth"
	message "github.com/viant/agently-core/internal/store/conversation"
)

func TestWorkspaceRuntimeMessageLookupInputMappings(t *testing.T) {
	fixture, _, db := orphanFixture(t)
	for _, statement := range []string{
		"INSERT INTO conversation(id,created_at,status) VALUES('lookup-main','2026-01-01T00:00:00Z','succeeded'),('lookup-linked','2026-01-01T00:00:00Z','succeeded')",
		"INSERT INTO message(id,conversation_id,role,type,created_at) VALUES('lookup-parent','lookup-main','user','text','2026-01-01T00:00:00Z')",
		"INSERT INTO message(id,conversation_id,role,type,elicitation_id,parent_message_id,linked_conversation_id,created_at) VALUES('lookup-match','lookup-main','assistant','elicitation_request','lookup-elic','lookup-parent','lookup-linked','2026-01-01T00:00:00Z')",
	} {
		_, err := db.Exec(statement)
		require.NoError(t, err)
	}
	store := &message.MessageStore{Invoker: fixture.Invoker, OwnerID: authctx.EffectiveUserID}
	ctx := context.Background()
	row, err := store.ByElicitation(ctx, "lookup-main", "lookup-elic")
	require.NoError(t, err)
	require.NotNil(t, row)
	require.Equal(t, "lookup-match", row.Id)
	row, err = store.ByParentElicitation(ctx, "lookup-parent", "lookup-elic")
	require.NoError(t, err)
	require.NotNil(t, row)
	require.Equal(t, "lookup-match", row.Id)
	row, err = store.ByLinkedElicitation(ctx, "lookup-linked", "lookup-elic")
	require.NoError(t, err)
	require.NotNil(t, row)
	require.Equal(t, "lookup-match", row.Id)
	row, err = store.ByElicitation(ctx, "lookup-linked", "lookup-elic")
	require.NoError(t, err)
	require.Nil(t, row, "conversation input mustscope lookup")
	row, err = store.ByParentElicitation(ctx, "lookup-parent", "missing-elic")
	require.NoError(t, err)
	require.Nil(t, row)
	row, err = store.ByLinkedElicitation(ctx, "lookup-linked", "")
	require.NoError(t, err)
	require.Nil(t, row)
}
