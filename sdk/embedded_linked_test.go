package sdk

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/store/conversation"
	convmem "github.com/viant/agently-core/internal/service/conversation/memory"
)

func TestEmbeddedClient_ListLinkedConversations_ExcludesOrphans(t *testing.T) {
	ctx := context.Background()
	convClient := convmem.New()

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

	client := &backendClient{
		conv: convClient,
	}

	page, err := client.ListLinkedConversations(ctx, &ListLinkedConversationsInput{
		ParentConversationID: "parent-1",
	})
	require.NoError(t, err)
	require.NotNil(t, page)
	require.Len(t, page.Rows, 1)
	require.Equal(t, "child-valid", page.Rows[0].ConversationID)
}

func TestEmbeddedClient_CreateConversation_PreservesParentLink(t *testing.T) {
	ctx := context.Background()
	convClient := convmem.New()

	client := &backendClient{
		conv: convClient,
	}

	parentTurnID := "parent-turn-1"
	created, err := client.CreateConversation(ctx, &CreateConversationInput{
		AgentID:              "coder",
		Title:                "child",
		ParentConversationID: "parent-1",
		ParentTurnID:         parentTurnID,
	})
	require.NoError(t, err)
	require.NotNil(t, created)
	require.Equal(t, "parent-1", ptrValue(created.ConversationParentId))
	require.Equal(t, parentTurnID, ptrValue(created.ConversationParentTurnId))
}

func TestEmbeddedClient_ListConversations_ConvFallbackAppliesBackendFilters(t *testing.T) {
	ctx := context.Background()
	convClient := convmem.New()

	rootVisible := conversation.NewConversation()
	rootVisible.SetId("root-visible")
	rootVisible.SetAgentId("agent-a")
	rootVisible.SetTitle("Favorite Colors")
	rootVisible.SetSummary("public summary")
	rootVisible.SetStatus("active")
	rootVisible.SetCreatedAt(nowPlus(1))
	rootVisible.SetVisibility("public")
	require.NoError(t, convClient.PatchConversations(ctx, rootVisible))

	rootFilteredByQuery := conversation.NewConversation()
	rootFilteredByQuery.SetId("root-query-miss")
	rootFilteredByQuery.SetAgentId("agent-a")
	rootFilteredByQuery.SetTitle("Different Topic")
	rootFilteredByQuery.SetStatus("active")
	rootFilteredByQuery.SetCreatedAt(nowPlus(2))
	rootFilteredByQuery.SetVisibility("public")
	require.NoError(t, convClient.PatchConversations(ctx, rootFilteredByQuery))

	rootFilteredByStatus := conversation.NewConversation()
	rootFilteredByStatus.SetId("root-status-miss")
	rootFilteredByStatus.SetAgentId("agent-a")
	rootFilteredByStatus.SetTitle("Favorite Colors Old")
	rootFilteredByStatus.SetStatus("completed")
	rootFilteredByStatus.SetCreatedAt(nowPlus(3))
	rootFilteredByStatus.SetVisibility("public")
	require.NoError(t, convClient.PatchConversations(ctx, rootFilteredByStatus))

	scheduled := conversation.NewConversation()
	scheduled.SetId("scheduled-hit")
	scheduled.SetAgentId("agent-a")
	scheduled.SetTitle("Favorite Colors Scheduled")
	scheduled.SetStatus("active")
	scheduled.SetCreatedAt(nowPlus(4))
	scheduled.SetVisibility("public")
	scheduled.SetScheduleId("sched-1")
	require.NoError(t, convClient.PatchConversations(ctx, scheduled))

	parent := conversation.NewConversation()
	parent.SetId("parent-1")
	parent.SetCreatedAt(nowPlus(5))
	parent.SetVisibility("public")
	require.NoError(t, convClient.PatchConversations(ctx, parent))

	parentTurn := conversation.NewTurn()
	parentTurn.SetId("parent-turn-1")
	parentTurn.SetConversationID("parent-1")
	parentTurn.SetStatus("completed")
	require.NoError(t, convClient.PatchTurn(ctx, parentTurn))

	child := conversation.NewConversation()
	child.SetId("child-1")
	child.SetAgentId("agent-a")
	child.SetTitle("Favorite Colors Child")
	child.SetStatus("active")
	child.SetConversationParentId("parent-1")
	child.SetConversationParentTurnId("parent-turn-1")
	child.SetCreatedAt(nowPlus(6))
	child.SetVisibility("public")
	require.NoError(t, convClient.PatchConversations(ctx, child))

	client := &backendClient{conv: convClient}

	page, err := client.ListConversations(ctx, &ListConversationsInput{
		AgentID:          "agent-a",
		ExcludeScheduled: true,
		Query:            "favorite",
		Status:           "active",
	})
	require.NoError(t, err)
	require.NotNil(t, page)
	require.Len(t, page.Rows, 1)
	require.Equal(t, "root-visible", page.Rows[0].Id)
}

func ptrValue[T any](value *T) T {
	var zero T
	if value == nil {
		return zero
	}
	return *value
}

func nowPlus(minutes int) time.Time {
	return time.Date(2026, 1, 1, 9, minutes, 0, 0, time.UTC)
}
