package data

import (
	"context"
	"reflect"
	"sort"
	"strings"
	"time"

	authctx "github.com/viant/agently-core/internal/auth"
	agconvlist "github.com/viant/agently-core/pkg/agently/conversation/list"
	agmessagelist "github.com/viant/agently-core/pkg/agently/message/list"
	agrunsteps "github.com/viant/agently-core/pkg/agently/run/steps"
	agturnlistall "github.com/viant/agently-core/pkg/agently/turn/list"
	"github.com/viant/datly"
	hstate "github.com/viant/xdatly/handler/state"
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
	Rows       []*agconvlist.ConversationRowsView
	NextCursor string
	PrevCursor string
	HasMore    bool
}

type MessagePage struct {
	Rows       []*agmessagelist.MessageRowsView
	NextCursor string
	PrevCursor string
	HasMore    bool
}

type TurnPage struct {
	Rows       []*agturnlistall.TurnRowsView
	NextCursor string
	PrevCursor string
	HasMore    bool
}

type RunStepPage struct {
	Rows       []*agrunsteps.RunStepsView
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

func buildPageSelector(viewName string, limit int) Option {
	return WithQuerySelector(&hstate.NamedQuerySelector{
		Name: viewName,
		QuerySelector: hstate.QuerySelector{
			Limit: limit + 1, // read one extra row to calculate hasMore
		},
	})
}

func buildMessagePageSelector(limit int) Option {
	return WithQuerySelector(&hstate.NamedQuerySelector{
		Name: "message_rows",
		QuerySelector: hstate.QuerySelector{
			Limit:   limit + 1, // read one extra row to calculate hasMore
			OrderBy: "created_at DESC,id DESC",
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

func conversationPageSortKey(row *agconvlist.ConversationRowsView) time.Time {
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

func sortConversationRows(rows []*agconvlist.ConversationRowsView) {
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

func hasNamedSelector(opts []Option, names ...string) bool {
	callOpts := collectOptions(opts)
	if len(callOpts.selectors) == 0 {
		return false
	}
	for _, selector := range callOpts.selectors {
		if selector == nil {
			continue
		}
		for _, name := range names {
			if selector.Name == name {
				return true
			}
		}
	}
	return false
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
	callOpts := collectOptions(opts)
	if callOpts.principal != "" {
		ctx = authctx.WithUserInfo(ctx, &authctx.UserInfo{Subject: callOpts.principal})
	} else if input.Has != nil {
		// Preserve prior unscoped list behavior for callers that don't set a principal.
		input.DefaultPredicate = "1"
		input.Has.DefaultPredicate = true
	}
	// Make the generated input available to predicate handlers such as
	// *conversationlist.Filter, which resolve visibility constraints from
	// InputFromContext(ctx). Without this, principal-aware visibility is applied
	// only after the SQL page is already truncated, causing underfilled pages
	// when recent rows belong to other users (for example scheduler traffic).
	ctx = context.WithValue(ctx, reflect.TypeOf(&agconvlist.ConversationRowsInput{}), &input)
	queryInput := input
	if queryInput.Has != nil && queryInput.Has.ExcludeScheduled {
		hasCopy := *queryInput.Has
		hasCopy.ExcludeScheduled = false
		queryInput.Has = &hasCopy
	}

	filterVisible := func(rows []*agconvlist.ConversationRowsView) []*agconvlist.ConversationRowsView {
		if callOpts.principal == "" || callOpts.isAdmin {
			filtered := make([]*agconvlist.ConversationRowsView, 0, len(rows))
			for _, row := range rows {
				if row == nil {
					continue
				}
				if input.ExcludeScheduled && row.ScheduleId != nil && strings.TrimSpace(*row.ScheduleId) != "" {
					continue
				}
				if (in == nil || in.Has == nil || !in.Has.ParentId) && row.ConversationParentId != nil && strings.TrimSpace(*row.ConversationParentId) != "" {
					continue
				}
				filtered = append(filtered, row)
			}
			return filtered
		}
		filtered := make([]*agconvlist.ConversationRowsView, 0, len(rows))
		for _, row := range rows {
			if row == nil {
				continue
			}
			if input.ExcludeScheduled && row.ScheduleId != nil && strings.TrimSpace(*row.ScheduleId) != "" {
				continue
			}
			if (in == nil || in.Has == nil || !in.Has.ParentId) && row.ConversationParentId != nil && strings.TrimSpace(*row.ConversationParentId) != "" {
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
		return filtered
	}

	fetchPage := func(batchInput *agconvlist.ConversationRowsInput) ([]*agconvlist.ConversationRowsView, bool, string, string, error) {
		out := &agconvlist.ConversationRowsOutput{}
		selectorOpts := append([]Option{buildPageSelector("ConversationRows", limit)}, opts...)
		operateOpts := append([]datly.OperateOption{
			datly.WithURI(agconvlist.ConversationRowsPathURI),
			datly.WithInput(batchInput),
			datly.WithOutput(out),
		}, toOperateOptions(selectorOpts)...)
		if _, err := s.dao.Operate(ctx, operateOpts...); err != nil {
			return nil, false, "", "", err
		}
		page := buildConversationPage(out.Data, limit)
		return out.Data, page.HasMore, page.NextCursor, page.PrevCursor, nil
	}

	rawRows, rawHasMore, nextCursor, prevCursor, err := fetchPage(&queryInput)
	if err != nil {
		return nil, err
	}
	rows := filterVisible(rawRows)
	sortConversationRows(rows)
	if callOpts.principal == "" || callOpts.isAdmin || len(rows) > limit || !rawHasMore {
		page := buildConversationPage(rows, limit)
		if !page.HasMore {
			page.HasMore = rawHasMore && len(rows) == limit
		}
		return page, nil
	}

	visible := append([]*agconvlist.ConversationRowsView{}, rows...)
	hasMoreRaw := rawHasMore

	for hasMoreRaw && len(visible) <= limit {
		nextInput := input
		if nextInput.Has == nil {
			nextInput.Has = &agconvlist.ConversationRowsInputHas{}
		} else {
			hasCopy := *nextInput.Has
			nextInput.Has = &hasCopy
		}
		switch direction {
		case DirectionAfter:
			if prevCursor == "" {
				hasMoreRaw = false
				continue
			}
			nextInput.CursorAfter = prevCursor
			nextInput.Has.CursorAfter = true
			nextInput.CursorBefore = ""
			nextInput.Has.CursorBefore = false
		default:
			if nextCursor == "" {
				hasMoreRaw = false
				continue
			}
			nextInput.CursorBefore = nextCursor
			nextInput.Has.CursorBefore = true
			nextInput.CursorAfter = ""
			nextInput.Has.CursorAfter = false
		}
		nextQueryInput := nextInput
		if nextQueryInput.Has != nil && nextQueryInput.Has.ExcludeScheduled {
			hasCopy := *nextQueryInput.Has
			hasCopy.ExcludeScheduled = false
			nextQueryInput.Has = &hasCopy
		}
		ctx = context.WithValue(ctx, reflect.TypeOf(&agconvlist.ConversationRowsInput{}), &nextInput)
		rawRows, rawHasMore, nextCursor, prevCursor, err = fetchPage(&nextQueryInput)
		if err != nil {
			return nil, err
		}
		visible = append(visible, filterVisible(rawRows)...)
		hasMoreRaw = rawHasMore
	}
	sortConversationRows(visible)

	result := buildConversationPage(visible, limit)
	if len(visible) > limit {
		result.HasMore = true
	}
	return result, nil
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
	selectorOpts := opts
	if !hasNamedSelector(opts, "message_rows", "MessageRows") {
		selectorOpts = append([]Option{buildMessagePageSelector(limit)}, opts...)
	}
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
