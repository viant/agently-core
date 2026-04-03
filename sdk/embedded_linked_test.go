package sdk

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/app/store/data"
	convsvc "github.com/viant/agently-core/internal/service/conversation"
)

func TestEmbeddedClient_ListLinkedConversations_ExcludesOrphans(t *testing.T) {
	ctx := context.Background()
	dao, err := data.NewDatlyInMemory(ctx)
	require.NoError(t, err)

	convClient, err := convsvc.New(ctx, dao)
	require.NoError(t, err)

	parent := conversation.NewConversation()
	parent.SetId("parent-1")
	require.NoError(t, convClient.PatchConversations(ctx, parent))

	parentTurn := conversation.NewTurn()
	parentTurn.SetId("parent-turn-1")
	parentTurn.SetConversationID("parent-1")
	parentTurn.SetStatus("completed")
	require.NoError(t, convClient.PatchTurn(ctx, parentTurn))

	childValid := conversation.NewConversation()
	childValid.SetId("child-valid")
	childValid.SetConversationParentId("parent-1")
	childValid.SetConversationParentTurnId("parent-turn-1")
	require.NoError(t, convClient.PatchConversations(ctx, childValid))

	childOrphanConv := conversation.NewConversation()
	childOrphanConv.SetId("child-orphan-conv")
	childOrphanConv.SetConversationParentId("missing-parent")
	childOrphanConv.SetConversationParentTurnId("parent-turn-1")
	require.NoError(t, convClient.PatchConversations(ctx, childOrphanConv))

	childOrphanTurn := conversation.NewConversation()
	childOrphanTurn.SetId("child-orphan-turn")
	childOrphanTurn.SetConversationParentId("parent-1")
	childOrphanTurn.SetConversationParentTurnId("missing-parent-turn")
	require.NoError(t, convClient.PatchConversations(ctx, childOrphanTurn))

	client := &EmbeddedClient{
		conv: convClient,
		data: data.NewService(dao),
	}

	page, err := client.ListLinkedConversations(ctx, &ListLinkedConversationsInput{
		ParentConversationID: "parent-1",
	})
	require.NoError(t, err)
	require.NotNil(t, page)
	require.Len(t, page.Rows, 1)
	require.Equal(t, "child-valid", page.Rows[0].ConversationID)
}
