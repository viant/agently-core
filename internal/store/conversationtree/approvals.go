package conversationtree

import (
	"context"
	"fmt"

	msgread "github.com/viant/agently-core/internal/datly/message/read"
	approvalread "github.com/viant/agently-core/internal/datly/toolapprovalqueue/read"
	turnread "github.com/viant/agently-core/internal/datly/turn/read"
	conversation "github.com/viant/agently-core/internal/store/conversation"
	"github.com/viant/xdatly/state"
)

// CollectApprovalIDs finds every approval linked to the graph before child
// rows are removed. The deletion transaction later passes these IDs to the
// canonical generated approval writer.
func (d *Discoverer) CollectApprovalIDs(ctx context.Context, graph *Graph) ([]string, error) {
	if d == nil || d.Invoker == nil || d.OwnerID == nil {
		return nil, fmt.Errorf("conversation graph reader is not configured")
	}
	if err := d.authorize(ctx, graph); err != nil {
		return nil, err
	}
	if len(graph.Nodes) == 0 {
		return nil, nil
	}
	conversationIDs := sortedMapKeys(graph.Nodes)
	turnQuery := &turnread.TurnRowsInput{}
	turnQuery.SetConversationIDs(conversationIDs)
	turnRows, err := (&conversation.TurnStore{Invoker: d.Invoker}).ListRows(ctx, turnQuery, nil)
	if err != nil {
		return nil, err
	}
	turnIDs := make([]string, 0, len(turnRows))
	for _, row := range turnRows {
		if row != nil {
			turnIDs = append(turnIDs, row.Id)
		}
	}
	messageQuery := &msgread.MessagesInput{}
	messageQuery.SetConversationIds(conversationIDs)
	messages, err := (&conversation.MessageStore{Invoker: d.Invoker, OwnerID: d.OwnerID}).ListRows(ctx, messageQuery,
		state.Selectors{&state.NamedSelector{Name: "reader", Selector: state.Selector{Fields: conversation.BaseMessageFields()}}})
	if err != nil {
		return nil, err
	}
	messageIDs := make([]string, 0, len(messages))
	for _, row := range messages {
		if row != nil {
			messageIDs = append(messageIDs, row.Id)
		}
	}
	queries := []*approvalread.ApprovalRowsInput{}
	byConversation := &approvalread.ApprovalRowsInput{}
	byConversation.SetConversationIds(conversationIDs)
	queries = append(queries, byConversation)
	if turnIDs = normalizeIDs(turnIDs); len(turnIDs) > 0 {
		byTurn := &approvalread.ApprovalRowsInput{}
		byTurn.SetTurnIds(turnIDs)
		queries = append(queries, byTurn)
	}
	if messageIDs = normalizeIDs(messageIDs); len(messageIDs) > 0 {
		byMessage := &approvalread.ApprovalRowsInput{}
		byMessage.SetMessageIds(messageIDs)
		queries = append(queries, byMessage)
	}
	store := &conversation.ApprovalStore{Invoker: d.Invoker, OwnerID: d.OwnerID}
	ids := []string{}
	for _, query := range queries {
		rows, err := store.List(ctx, "rows", query, nil)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			if row != nil {
				ids = append(ids, row.Id)
			}
		}
	}
	return normalizeIDs(ids), nil
}
