package data

import (
	"context"

	read "github.com/viant/agently-core/internal/datly/turn/read"
	store "github.com/viant/agently-core/internal/store/conversation"
	turnmodel "github.com/viant/agently-core/model/turn"
	"github.com/viant/xdatly/state"
)

func nativeTurnPageInput(input *turnmodel.TurnRowsInput) *read.TurnRowsInput {
	query := &read.TurnRowsInput{}
	if input == nil || input.Has == nil {
		return query
	}
	h := input.Has
	if h.ConversationID {
		query.SetConversationID(input.ConversationID)
	}
	if h.TurnId {
		query.SetTurnId(input.TurnId)
	}
	if h.Statuses {
		query.SetStatuses(input.Statuses)
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
	return query
}

func (s *datlyService) queryTurnRowsNative(ctx context.Context, input *turnmodel.TurnRowsInput, limit int, opts *options) ([]*turnmodel.TurnRowsView, error) {
	selectors := state.Selectors{&state.NamedSelector{Name: "TurnRows", Selector: state.Selector{Limit: limit + 1}}}
	if opts != nil {
		selectors = append(selectors, nativeSelectors(opts.selectors)...)
	}
	rows, err := (&store.TurnStore{Invoker: s.native}).ListRows(ctx, nativeTurnPageInput(input), selectors)
	if err != nil {
		return nil, err
	}
	return rows, nil
}
