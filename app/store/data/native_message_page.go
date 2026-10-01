package data

import (
	"context"
	"encoding/json"
	"fmt"

	authctx "github.com/viant/agently-core/internal/auth"
	read "github.com/viant/agently-core/internal/datly/message/read"
	store "github.com/viant/agently-core/internal/store/conversation"
	legacy "github.com/viant/agently-core/pkg/agently/message/list"
	"github.com/viant/xdatly/state"
)

func nativeMessagePageInput(input *legacy.MessageRowsInput) *read.MessagesInput {
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

func (s *datlyService) queryMessageRowsNative(ctx context.Context, input *legacy.MessageRowsInput, limit int, callOpts *options) ([]*legacy.MessageRowsView, error) {
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
			if len(selector.Fields) == 0 {
				selector.Fields = store.BaseMessageFields()
			}
		}
	}
	if !hasPageSelector {
		selectors = append(selectors, &state.NamedSelector{Name: "reader", Selector: state.Selector{
			Fields: store.BaseMessageFields(), OrderBy: "created_at DESC,id DESC", Limit: limit + 1,
		}})
	}
	component := &store.MessageStore{Invoker: s.native, OwnerID: authctx.EffectiveUserID}
	rows, err := component.ListRows(ctx, nativeMessagePageInput(input), selectors)
	if err != nil {
		return nil, err
	}
	result := make([]*legacy.MessageRowsView, 0, len(rows))
	for _, row := range rows {
		if row == nil {
			continue
		}
		encoded, err := json.Marshal(row)
		if err != nil {
			return nil, fmt.Errorf("encode native message row: %w", err)
		}
		var mapped legacy.MessageRowsView
		if err := json.Unmarshal(encoded, &mapped); err != nil {
			return nil, fmt.Errorf("decode message row contract: %w", err)
		}
		result = append(result, &mapped)
	}
	return result, nil
}
