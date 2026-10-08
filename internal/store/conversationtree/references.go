package conversationtree

import (
	"context"
	"errors"
	"fmt"

	convread "github.com/viant/agently-core/internal/datly/conversation/read"
	msgread "github.com/viant/agently-core/internal/datly/message/read"
	turnread "github.com/viant/agently-core/internal/datly/turn/read"
	conversation "github.com/viant/agently-core/internal/store/conversation"
	"github.com/viant/xdatly/state"
)

var ErrGraphReferenced = errors.New("conversation graph is referenced from outside the graph")

// ValidateInboundLinks rejects messages outside the graph that link into it,
// and parent-turn children that appeared after discovery. The custom deletion
// component will repeat this inside its managed transaction before mutation.
func (d *Discoverer) ValidateInboundLinks(ctx context.Context, graph *Graph) error {
	if d == nil || d.Invoker == nil || d.OwnerID == nil {
		return fmt.Errorf("conversation graph reader is not configured")
	}
	if err := d.authorize(ctx, graph); err != nil {
		return err
	}
	var err error
	ctx, err = PinGraphReader(ctx)
	if err != nil {
		return err
	}
	if len(graph.Nodes) == 0 {
		return nil
	}
	conversationIDs := sortedMapKeys(graph.Nodes)
	messages := &conversation.MessageStore{Invoker: d.Invoker, OwnerID: d.OwnerID}
	query := &msgread.MessagesInput{}
	query.SetLinkedConversationIds(conversationIDs)
	selector := state.Selectors{&state.NamedSelector{Name: "reader", Selector: state.Selector{Fields: []string{"id", "conversation_id", "linked_conversation_id"}}}}
	rows, err := messages.ListRows(ctx, query, selector)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row != nil && graph.Nodes[row.ConversationId] == nil {
			return ErrGraphReferenced
		}
	}
	turnQuery := &turnread.TurnRowsInput{}
	turnQuery.SetConversationIDs(conversationIDs)
	turns, err := (&conversation.TurnStore{Invoker: d.Invoker}).ListRows(ctx, turnQuery, deleteSelectors("id"))
	if err != nil {
		return err
	}
	turnIDs := make([]string, 0, len(turns))
	for _, row := range turns {
		if row != nil {
			turnIDs = append(turnIDs, row.Id)
		}
	}
	if turnIDs = normalizeIDs(turnIDs); len(turnIDs) > 0 {
		childrenQuery := &convread.ConversationInput{}
		childrenQuery.SetParentTurnIds(turnIDs)
		children, err := d.graphRows(ctx, childrenQuery)
		if err != nil {
			return err
		}
		for _, row := range children {
			if row != nil && graph.Nodes[row.Id] == nil {
				return ErrGraphReferenced
			}
		}
	}
	goalIDs, err := d.goalIDs(ctx, conversationIDs)
	if err != nil {
		return err
	}
	if len(goalIDs) > 0 {
		goalTurns := &turnread.TurnRowsInput{}
		goalTurns.SetGoalIDs(goalIDs)
		rows, err := (&conversation.TurnStore{Invoker: d.Invoker}).ListRows(ctx, goalTurns, deleteSelectors("id", "conversation_id"))
		if err != nil {
			return err
		}
		for _, row := range rows {
			if row != nil && graph.Nodes[row.ConversationId] == nil {
				return ErrGraphReferenced
			}
		}
	}
	return nil
}
