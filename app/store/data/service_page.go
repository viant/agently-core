package data

import (
	"context"
	"sort"
	"strings"
	"time"

	authctx "github.com/viant/agently-core/internal/auth"
	conversationmodel "github.com/viant/agently-core/model/conversation"
	messagemodel "github.com/viant/agently-core/model/message"
	runmodel "github.com/viant/agently-core/model/run"
	turnmodel "github.com/viant/agently-core/model/turn"
)

type Direction string

const (
	DirectionBefore Direction = "before"
	DirectionAfter  Direction = "after"
	DirectionLatest Direction = "latest"
)

type PageInput struct {
	Limit     int
	Cursor    string
	Direction Direction
}

type ConversationPage struct {
	Rows       []*conversationmodel.ConversationRowsView
	NextCursor string
	PrevCursor string
	HasMore    bool
	HasOlder   bool
	HasNewer   bool
}

type MessagePage struct {
	Rows       []*messagemodel.MessageRowsView
	NextCursor string
	PrevCursor string
	HasMore    bool
}

type TurnPage struct {
	Rows       []*turnmodel.TurnRowsView
	NextCursor string
	PrevCursor string
	HasMore    bool
}

type RunStepPage struct {
	Rows       []*runmodel.RunStepsView
	NextCursor string
	PrevCursor string
	HasMore    bool
}

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

func buildConversationPage(rows []*conversationmodel.ConversationRowsView, limit int, direction Direction, cursor string) *ConversationPage {
	page := &ConversationPage{Rows: rows}
	if len(rows) > limit {
		page.HasMore = true
		page.Rows = rows[:limit]
	}
	if len(page.Rows) > 0 {
		page.PrevCursor = page.Rows[0].Id
		page.NextCursor = page.Rows[len(page.Rows)-1].Id
	}
	hasCursor := strings.TrimSpace(cursor) != "" && len(page.Rows) > 0
	switch direction {
	case DirectionAfter:
		page.HasNewer = page.HasMore
		page.HasOlder = hasCursor
	case DirectionLatest:
		page.HasOlder = page.HasMore
	default:
		page.HasOlder = page.HasMore
		page.HasNewer = hasCursor
	}
	return page
}

func buildConversationAfterPage(rows []*conversationmodel.ConversationRowsView, limit int, cursor string) *ConversationPage {
	page := &ConversationPage{}
	if len(rows) > limit {
		page.HasMore = true
		rows = rows[:limit]
	}
	sortConversationRows(rows)
	page.Rows = rows
	if len(page.Rows) > 0 {
		page.PrevCursor = page.Rows[0].Id
		page.NextCursor = page.Rows[len(page.Rows)-1].Id
	}
	hasCursor := strings.TrimSpace(cursor) != "" && len(page.Rows) > 0
	page.HasNewer = page.HasMore
	page.HasOlder = hasCursor
	return page
}

func conversationPageSortKey(row *conversationmodel.ConversationRowsView) time.Time {
	if row == nil {
		return time.Time{}
	}
	if row.LastActivity != nil && !row.LastActivity.IsZero() {
		return *row.LastActivity
	}
	if row.UpdatedAt != nil && !row.UpdatedAt.IsZero() {
		return *row.UpdatedAt
	}
	return row.CreatedAt
}

func sortConversationRows(rows []*conversationmodel.ConversationRowsView) {
	sort.SliceStable(rows, func(i, j int) bool {
		left := rows[i]
		right := rows[j]
		leftTime := conversationPageSortKey(left)
		rightTime := conversationPageSortKey(right)
		if leftTime.Equal(rightTime) {
			leftID := ""
			rightID := ""
			if left != nil {
				leftID = left.Id
			}
			if right != nil {
				rightID = right.Id
			}
			return leftID > rightID
		}
		return leftTime.After(rightTime)
	})
}

func buildMessagePage(rows []*messagemodel.MessageRowsView, limit int) *MessagePage {
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

func buildTurnPage(rows []*turnmodel.TurnRowsView, limit int) *TurnPage {
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

func buildRunStepPage(rows []*runmodel.RunStepsView, limit int) *RunStepPage {
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

func (s *datlyService) ListConversations(ctx context.Context, in *conversationmodel.ConversationRowsInput, page *PageInput, opts ...Option) (*ConversationPage, error) {
	input := conversationmodel.ConversationRowsInput{Has: &conversationmodel.ConversationRowsInputHas{}}
	if in != nil {
		input = *in
		if input.Has == nil {
			input.Has = &conversationmodel.ConversationRowsInputHas{}
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
	callOpts := collectOptions(opts)
	if callOpts.principal != "" {
		ctx = authctx.WithUserInfo(ctx, &authctx.UserInfo{Subject: callOpts.principal})
	}
	if !input.Has.ParentId && !input.Has.ParentTurnId {
		input.ExcludeChildren = true
		input.Has.ExcludeChildren = true
	}
	rows, err := s.queryConversationRows(ctx, &input, limit+1, direction, callOpts)
	if err != nil {
		return nil, err
	}
	if direction == DirectionAfter {
		return buildConversationAfterPage(rows, limit, cursor), nil
	}
	sortConversationRows(rows)
	return buildConversationPage(rows, limit, direction, cursor), nil
}

func (s *datlyService) queryConversationRows(ctx context.Context, input *conversationmodel.ConversationRowsInput, limit int, direction Direction, callOpts *options) ([]*conversationmodel.ConversationRowsView, error) {
	return s.queryConversationRowsNative(ctx, input, limit, direction, callOpts)
}

func (s *datlyService) GetMessagesPage(ctx context.Context, in *messagemodel.MessageRowsInput, page *PageInput, opts ...Option) (*MessagePage, error) {
	input := messagemodel.MessageRowsInput{Has: &messagemodel.MessageRowsInputHas{}}
	if in != nil {
		input = *in
		if input.Has == nil {
			input.Has = &messagemodel.MessageRowsInputHas{}
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
	callOpts := collectOptions(opts)
	rows, err := s.queryMessageRowsNative(ctx, &input, limit, callOpts)
	if err != nil {
		return nil, err
	}
	if callOpts.principal != "" && !callOpts.isAdmin {
		cache := newAuthCache()
		filtered := make([]*messagemodel.MessageRowsView, 0, len(rows))
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

func (s *datlyService) GetTurnsPage(ctx context.Context, in *turnmodel.TurnRowsInput, page *PageInput, opts ...Option) (*TurnPage, error) {
	input := turnmodel.TurnRowsInput{Has: &turnmodel.TurnRowsInputHas{}}
	if in != nil {
		input = *in
		if input.Has == nil {
			input.Has = &turnmodel.TurnRowsInputHas{}
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

	callOpts := collectOptions(opts)
	rows, err := s.queryTurnRowsNative(ctx, &input, limit, callOpts)
	if err != nil {
		return nil, err
	}
	if callOpts.principal != "" && !callOpts.isAdmin {
		cache := newAuthCache()
		filtered := make([]*turnmodel.TurnRowsView, 0, len(rows))
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

func (s *datlyService) GetRunStepsPage(ctx context.Context, in *runmodel.RunStepsInput, page *PageInput, opts ...Option) (*RunStepPage, error) {
	input := runmodel.RunStepsInput{Has: &runmodel.RunStepsInputHas{}}
	if in != nil {
		input = *in
		if input.Has == nil {
			input.Has = &runmodel.RunStepsInputHas{}
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
	rows, err := s.queryRunStepsNative(ctx, &input, limit, resolvedOpts)
	if err != nil {
		return nil, err
	}
	if resolvedOpts.principal != "" && !resolvedOpts.isAdmin {
		cache := newAuthCache()
		filtered := make([]*runmodel.RunStepsView, 0, len(rows))
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
