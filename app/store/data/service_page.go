package data

import (
	"context"
	"strings"

	agconvlist "github.com/viant/agently-core/pkg/agently/conversation/list"
	agmessagelist "github.com/viant/agently-core/pkg/agently/message/list"
	agrunsteps "github.com/viant/agently-core/pkg/agently/run/steps"
	agturnlistall "github.com/viant/agently-core/pkg/agently/turn/list"
	"github.com/viant/datly"
	hstate "github.com/viant/xdatly/handler/state"
)

func normalizePageInput(page *PageInput) (int, Direction, string) {
	if page == nil {
		return 50, DirectionBefore, ""
	}
	limit := page.Limit
	if limit <= 0 {
		limit = 50
	}
	direction := page.Direction
	if direction == "" {
		direction = DirectionBefore
	}
	return limit, direction, page.Cursor
}

func buildPageSelector(viewName string, limit int) Option {
	return WithQuerySelector(&hstate.NamedQuerySelector{
		Name: viewName,
		QuerySelector: hstate.QuerySelector{
			Limit: limit + 1, // read one extra row to calculate hasMore
		},
	})
}

func buildConversationPage(rows []*agconvlist.ConversationRowsView, limit int) *ConversationPage {
	page := &ConversationPage{Rows: rows}
	if len(rows) > limit {
		page.HasMore = true
		page.Rows = rows[:limit]
	}
	if len(page.Rows) > 0 {
		page.PrevCursor = page.Rows[0].Id
		page.NextCursor = page.Rows[len(page.Rows)-1].Id
	}
	return page
}

func buildMessagePage(rows []*agmessagelist.MessageRowsView, limit int) *MessagePage {
	page := &MessagePage{Rows: rows}
	if len(rows) > limit {
		page.HasMore = true
		page.Rows = rows[:limit]
	}
	if len(page.Rows) > 0 {
		page.PrevCursor = page.Rows[0].Id
		page.NextCursor = page.Rows[len(page.Rows)-1].Id
	}
	return page
}

func buildTurnPage(rows []*agturnlistall.TurnRowsView, limit int) *TurnPage {
	page := &TurnPage{Rows: rows}
	if len(rows) > limit {
		page.HasMore = true
		page.Rows = rows[:limit]
	}
	if len(page.Rows) > 0 {
		page.PrevCursor = page.Rows[0].Id
		page.NextCursor = page.Rows[len(page.Rows)-1].Id
	}
	return page
}

func buildRunStepPage(rows []*agrunsteps.RunStepsView, limit int) *RunStepPage {
	page := &RunStepPage{Rows: rows}
	if len(rows) > limit {
		page.HasMore = true
		page.Rows = rows[:limit]
	}
	if len(page.Rows) > 0 {
		page.PrevCursor = page.Rows[0].MessageId
		page.NextCursor = page.Rows[len(page.Rows)-1].MessageId
	}
	return page
}

func (s *datlyService) ListConversations(ctx context.Context, in *agconvlist.ConversationRowsInput, page *PageInput, opts ...Option) (*ConversationPage, error) {
	input := agconvlist.ConversationRowsInput{Has: &agconvlist.ConversationRowsInputHas{}}
	if in != nil {
		input = *in
		if input.Has == nil {
			input.Has = &agconvlist.ConversationRowsInputHas{}
		}
	}
	limit, direction, cursor := normalizePageInput(page)
	switch direction {
	case DirectionAfter:
		if cursor != "" {
			input.CursorAfter = cursor
			input.Has.CursorAfter = true
		}
	case DirectionBefore:
		if cursor != "" {
			input.CursorBefore = cursor
			input.Has.CursorBefore = true
		}
	case DirectionLatest:
		// Latest page intentionally ignores cursor and returns the newest window.
	default:
		if cursor != "" {
			input.CursorBefore = cursor
			input.Has.CursorBefore = true
		}
	}

	out := &agconvlist.ConversationRowsOutput{}
	selectorOpts := append([]Option{buildPageSelector("ConversationRows", limit)}, opts...)
	operateOpts := append([]datly.OperateOption{
		datly.WithURI(agconvlist.ConversationRowsPathURI),
		datly.WithInput(&input),
		datly.WithOutput(out),
	}, toOperateOptions(selectorOpts)...)
	if _, err := s.dao.Operate(ctx, operateOpts...); err != nil {
		return nil, err
	}
	callOpts := collectOptions(opts)
	rows := out.Data
	if callOpts.principal != "" && !callOpts.isAdmin {
		filtered := make([]*agconvlist.ConversationRowsView, 0, len(rows))
		for _, row := range rows {
			if row == nil {
				continue
			}
			if strings.EqualFold(row.Visibility, "public") {
				filtered = append(filtered, row)
				continue
			}
			if row.CreatedByUserId != nil && *row.CreatedByUserId == callOpts.principal {
				filtered = append(filtered, row)
			}
		}
		rows = filtered
	}
	return buildConversationPage(rows, limit), nil
}

func (s *datlyService) GetMessagesPage(ctx context.Context, in *agmessagelist.MessageRowsInput, page *PageInput, opts ...Option) (*MessagePage, error) {
	input := agmessagelist.MessageRowsInput{Has: &agmessagelist.MessageRowsInputHas{}}
	if in != nil {
		input = *in
		if input.Has == nil {
			input.Has = &agmessagelist.MessageRowsInputHas{}
		}
	}
	limit, direction, cursor := normalizePageInput(page)
	switch direction {
	case DirectionAfter:
		if cursor != "" {
			input.CursorAfter = cursor
			input.Has.CursorAfter = true
		}
	case DirectionBefore:
		if cursor != "" {
			input.CursorBefore = cursor
			input.Has.CursorBefore = true
		}
	case DirectionLatest:
		// Latest page intentionally ignores cursor and returns the newest window.
	default:
		if cursor != "" {
			input.CursorBefore = cursor
			input.Has.CursorBefore = true
		}
	}

	out := &agmessagelist.MessageRowsOutput{}
	selectorOpts := append([]Option{buildPageSelector("MessageRows", limit)}, opts...)
	operateOpts := append([]datly.OperateOption{
		datly.WithURI(agmessagelist.MessageRowsPathURI),
		datly.WithInput(&input),
		datly.WithOutput(out),
	}, toOperateOptions(selectorOpts)...)
	if _, err := s.dao.Operate(ctx, operateOpts...); err != nil {
		return nil, err
	}
	callOpts := collectOptions(opts)
	rows := out.Data
	if callOpts.principal != "" && !callOpts.isAdmin {
		cache := newAuthCache()
		filtered := make([]*agmessagelist.MessageRowsView, 0, len(rows))
		for _, row := range rows {
			if row == nil {
				continue
			}
			if err := s.authorizeConversationID(ctx, row.ConversationId, callOpts, cache); err == nil {
				filtered = append(filtered, row)
			}
		}
		rows = filtered
	}
	return buildMessagePage(rows, limit), nil
}

func (s *datlyService) GetTurnsPage(ctx context.Context, in *agturnlistall.TurnRowsInput, page *PageInput, opts ...Option) (*TurnPage, error) {
	input := agturnlistall.TurnRowsInput{Has: &agturnlistall.TurnRowsInputHas{}}
	if in != nil {
		input = *in
		if input.Has == nil {
			input.Has = &agturnlistall.TurnRowsInputHas{}
		}
	}
	limit, direction, cursor := normalizePageInput(page)
	switch direction {
	case DirectionAfter:
		if cursor != "" {
			input.CursorAfter = cursor
			input.Has.CursorAfter = true
		}
	default:
		if cursor != "" {
			input.CursorBefore = cursor
			input.Has.CursorBefore = true
		}
	}

	out := &agturnlistall.TurnRowsOutput{}
	selectorOpts := append([]Option{buildPageSelector("TurnRows", limit)}, opts...)
	operateOpts := append([]datly.OperateOption{
		datly.WithURI(agturnlistall.TurnRowsPathURI),
		datly.WithInput(&input),
		datly.WithOutput(out),
	}, toOperateOptions(selectorOpts)...)
	if _, err := s.dao.Operate(ctx, operateOpts...); err != nil {
		return nil, err
	}
	callOpts := collectOptions(opts)
	rows := out.Data
	if callOpts.principal != "" && !callOpts.isAdmin {
		cache := newAuthCache()
		filtered := make([]*agturnlistall.TurnRowsView, 0, len(rows))
		for _, row := range rows {
			if row == nil {
				continue
			}
			if err := s.authorizeConversationID(ctx, row.ConversationId, callOpts, cache); err == nil {
				filtered = append(filtered, row)
			}
		}
		rows = filtered
	}
	return buildTurnPage(rows, limit), nil
}

func (s *datlyService) GetRunStepsPage(ctx context.Context, in *agrunsteps.RunStepsInput, page *PageInput, opts ...Option) (*RunStepPage, error) {
	input := agrunsteps.RunStepsInput{Has: &agrunsteps.RunStepsInputHas{}}
	if in != nil {
		input = *in
		if input.Has == nil {
			input.Has = &agrunsteps.RunStepsInputHas{}
		}
	}
	resolvedOpts := collectOptions(opts)
	if resolvedOpts.principal != "" && !resolvedOpts.isAdmin && input.RunID != "" {
		if _, err := s.GetRun(ctx, input.RunID, nil, opts...); err != nil {
			return nil, err
		}
	}
	limit, direction, cursor := normalizePageInput(page)
	switch direction {
	case DirectionAfter:
		if cursor != "" {
			input.CursorAfter = cursor
			input.Has.CursorAfter = true
		}
	default:
		if cursor != "" {
			input.CursorBefore = cursor
			input.Has.CursorBefore = true
		}
	}
	out := &agrunsteps.RunStepsOutput{}
	callOpts := append([]Option{buildPageSelector("RunSteps", limit)}, opts...)
	operateOpts := append([]datly.OperateOption{
		datly.WithURI(agrunsteps.RunStepsPathURI),
		datly.WithInput(&input),
		datly.WithOutput(out),
	}, toOperateOptions(callOpts)...)
	if _, err := s.dao.Operate(ctx, operateOpts...); err != nil {
		return nil, err
	}
	rows := out.Data
	if resolvedOpts.principal != "" && !resolvedOpts.isAdmin {
		cache := newAuthCache()
		filtered := make([]*agrunsteps.RunStepsView, 0, len(rows))
		for _, row := range rows {
			if row == nil || row.ConversationId == nil {
				continue
			}
			if err := s.authorizeConversationID(ctx, *row.ConversationId, resolvedOpts, cache); err == nil {
				filtered = append(filtered, row)
			}
		}
		rows = filtered
	}
	return buildRunStepPage(rows, limit), nil
}
