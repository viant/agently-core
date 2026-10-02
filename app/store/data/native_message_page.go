package data

import (
	"context"

	authctx "github.com/viant/agently-core/internal/auth"
	read "github.com/viant/agently-core/internal/datly/message/read"
	store "github.com/viant/agently-core/internal/store/conversation"
	messagemodel "github.com/viant/agently-core/model/message"
	"github.com/viant/xdatly/state"
)

func nativeMessagePageInput(input *messagemodel.MessageRowsInput) *read.MessagesInput {
	query := &read.MessagesInput{}
	if input == nil || input.Has == nil {
		return query
	}
	h := input.Has
	if h.ConversationId {
		query.SetConversationId(input.ConversationId)
	}
	if h.TurnId {
		query.SetTurnId(input.TurnId)
	}
	if h.Id {
		query.SetId(input.Id)
	}
	if h.Roles {
		query.SetRoles(input.Roles)
	}
	if h.Types {
		query.SetTypes(input.Types)
	}
	if h.Interim {
		query.SetInterim(input.Interim)
	}
	if h.Phase {
		query.SetPhase(input.Phase)
	}
	if h.Iteration {
		query.SetIteration(input.Iteration)
	}
	if h.CreatedSince {
		query.SetCreatedSince(input.CreatedSince)
	}
	if h.CreatedBefore {
		query.SetCreatedBefore(input.CreatedBefore)
	}
	if h.CursorBefore {
		query.SetCursorBefore(input.CursorBefore)
	}
	if h.CursorAfter {
		query.SetCursorAfter(input.CursorAfter)
	}
	if h.TurnTask {
		query.SetTurnTask(input.TurnTask)
	}
	if h.AssistantFinal {
		query.SetAssistantFinal(input.AssistantFinal)
	}
	if h.AssistantStatus {
		query.SetAssistantStatus(input.AssistantStatus)
	}
	if h.IncludeModelCal {
		query.SetIncludeModelCal(input.IncludeModelCal)
	}
	if h.IncludeToolCall {
		query.SetIncludeToolCall(input.IncludeToolCall)
	}
	return query
}

func (s *datlyService) queryMessageRowsNative(ctx context.Context, input *messagemodel.MessageRowsInput, limit int, callOpts *options) ([]*messagemodel.MessageRowsView, error) {
	selectors := state.Selectors(nil)
	if callOpts != nil {
		selectors = nativeSelectors(callOpts.selectors)
	}
	hasPageSelector := false
	for _, selector := range selectors {
		if selector == nil {
			continue
		}
		if selector.Name == "message_rows" || selector.Name == "MessageRows" || selector.Name == "reader" {
			hasPageSelector = true
		}
	}
	if !hasPageSelector {
		selectors = append(selectors, &state.NamedSelector{Name: "reader", Selector: state.Selector{
			OrderBy: "created_at DESC,id DESC", Limit: limit + 1,
		}})
	}
	component := &store.MessageStore{Invoker: s.native, OwnerID: authctx.EffectiveUserID}
	rows, err := component.ListBaseRows(ctx, nativeMessagePageInput(input), selectors)
	if err != nil {
		return nil, err
	}
	return rows, nil
}
