package data

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strconv"
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
	}
	if !input.Has.ParentId && !input.Has.ParentTurnId {
		input.ExcludeChildren = true
		input.Has.ExcludeChildren = true
	}
	rows, err := s.queryConversationRows(ctx, &input, limit+1, callOpts)
	if err != nil {
		return nil, err
	}
	sortConversationRows(rows)
	result := buildConversationPage(rows, limit)
	if direction == DirectionAfter {
		sortConversationRows(result.Rows)
	}
	return result, nil
}

func (s *datlyService) queryConversationRows(ctx context.Context, input *agconvlist.ConversationRowsInput, limit int, callOpts *options) ([]*agconvlist.ConversationRowsView, error) {
	db, err := s.db()
	if err != nil {
		return nil, err
	}
	query, args := buildConversationRowsQuery(input, limit, callOpts)
	sqlRows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer sqlRows.Close()

	rows := make([]*agconvlist.ConversationRowsView, 0, limit)
	for sqlRows.Next() {
		row, scanErr := scanConversationRowsView(sqlRows)
		if scanErr != nil {
			return nil, scanErr
		}
		rows = append(rows, row)
	}
	if err := sqlRows.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}

func buildConversationRowsQuery(input *agconvlist.ConversationRowsInput, limit int, callOpts *options) (string, []interface{}) {
	var builder strings.Builder
	latestTurnStatusExpr := `(SELECT LOWER(COALESCE(t.status, '')) FROM turn t WHERE t.conversation_id = c.id ORDER BY t.created_at DESC, t.id DESC LIMIT 1)`
	builder.WriteString(`SELECT
		(SELECT id FROM turn t WHERE t.conversation_id = c.id ORDER BY t.created_at DESC, t.id DESC LIMIT 1) AS last_turn_id,
		CASE
			WHEN COALESCE(` + latestTurnStatusExpr + `, LOWER(COALESCE(c.status, ''))) IN ('failed', 'error', 'terminated') THEN 'error'
			WHEN COALESCE(` + latestTurnStatusExpr + `, LOWER(COALESCE(c.status, ''))) IN ('canceled', 'cancelled') THEN 'canceled'
			WHEN COALESCE(` + latestTurnStatusExpr + `, LOWER(COALESCE(c.status, ''))) IN ('completed', 'succeeded', 'success', 'done', 'compacted', 'pruned') THEN 'done'
			WHEN COALESCE(` + latestTurnStatusExpr + `, LOWER(COALESCE(c.status, ''))) IN ('waiting_for_user', 'blocked') THEN 'elicitation'
			WHEN COALESCE(` + latestTurnStatusExpr + `, LOWER(COALESCE(c.status, ''))) IN ('running', 'thinking', 'processing', 'in_progress', 'queued', 'pending', 'open') THEN 'executing'
			ELSE ''
		END AS stage,
		c.id,
		c.summary,
		c.last_activity,
		c.usage_input_tokens,
		c.usage_output_tokens,
		c.usage_embedding_tokens,
		c.created_at,
		c.updated_at,
		c.created_by_user_id,
		c.agent_id,
		c.default_model_provider,
		c.default_model,
		c.default_model_params,
		c.title,
		c.conversation_parent_id,
		c.conversation_parent_turn_id,
		c.metadata,
		c.visibility,
		c.shareable,
		CASE
			WHEN ` + latestTurnStatusExpr + ` = 'completed' THEN 'succeeded'
			WHEN ` + latestTurnStatusExpr + ` = 'success' THEN 'succeeded'
			WHEN ` + latestTurnStatusExpr + ` = 'done' THEN 'succeeded'
			WHEN ` + latestTurnStatusExpr + ` <> '' THEN ` + latestTurnStatusExpr + `
			ELSE c.status
		END AS status,
		c.scheduled,
		c.schedule_id,
		c.schedule_run_id,
		c.schedule_kind,
		c.schedule_timezone,
		c.schedule_cron_expr,
		c.external_task_ref
	FROM conversation c
	WHERE (c.conversation_parent_id IS NULL OR (
		c.conversation_parent_turn_id IS NOT NULL
		AND EXISTS (SELECT 1 FROM conversation p WHERE p.id = c.conversation_parent_id)
		AND EXISTS (SELECT 1 FROM turn pt WHERE pt.id = c.conversation_parent_turn_id AND pt.conversation_id = c.conversation_parent_id)
	))`)
	args := make([]interface{}, 0, 24)

	if callOpts == nil || (callOpts.principal == "" && !callOpts.isAdmin) {
		// Unscoped internal readers preserve previous behavior and bypass visibility filtering.
	} else if !callOpts.isAdmin {
		builder.WriteString(" AND (COALESCE(c.visibility, '') <> ? OR c.created_by_user_id = ?)")
		args = append(args, "private", callOpts.principal)
	}

	if input != nil && input.Has != nil {
		if input.Has.AgentId && strings.TrimSpace(input.AgentId) != "" {
			builder.WriteString(" AND c.agent_id = ?")
			args = append(args, strings.TrimSpace(input.AgentId))
		}
		if input.Has.ParentId && strings.TrimSpace(input.ParentId) != "" {
			builder.WriteString(" AND c.conversation_parent_id = ?")
			args = append(args, strings.TrimSpace(input.ParentId))
		}
		if input.Has.ParentTurnId && strings.TrimSpace(input.ParentTurnId) != "" {
			builder.WriteString(" AND c.conversation_parent_turn_id = ?")
			args = append(args, strings.TrimSpace(input.ParentTurnId))
		}
		if input.Has.ExcludeChildren && input.ExcludeChildren {
			builder.WriteString(" AND c.conversation_parent_id IS NULL")
		}
		if input.Has.ExcludeScheduled && input.ExcludeScheduled {
			builder.WriteString(" AND c.schedule_id IS NULL")
		}
		if input.Has.ScheduleId && strings.TrimSpace(input.ScheduleId) != "" {
			builder.WriteString(" AND c.schedule_id = ?")
			args = append(args, strings.TrimSpace(input.ScheduleId))
		}
		if input.Has.ScheduleRunId && strings.TrimSpace(input.ScheduleRunId) != "" {
			builder.WriteString(" AND c.schedule_run_id = ?")
			args = append(args, strings.TrimSpace(input.ScheduleRunId))
		}
		if input.Has.Query && strings.TrimSpace(input.Query) != "" {
			builder.WriteString(" AND LOWER(c.id || ' ' || COALESCE(c.title, '') || ' ' || COALESCE(c.summary, '')) LIKE '%' || LOWER(?) || '%'")
			args = append(args, strings.TrimSpace(input.Query))
		}
		if input.Has.StatusFilter && strings.TrimSpace(input.StatusFilter) != "" {
			builder.WriteString(" AND c.status = ?")
			args = append(args, strings.TrimSpace(input.StatusFilter))
		}
		if input.Has.CreatedSince && !input.CreatedSince.IsZero() {
			builder.WriteString(" AND c.created_at >= ?")
			args = append(args, input.CreatedSince)
		}
		if input.Has.CreatedBefore && !input.CreatedBefore.IsZero() {
			builder.WriteString(" AND c.created_at <= ?")
			args = append(args, input.CreatedBefore)
		}
		if input.Has.CursorBefore && strings.TrimSpace(input.CursorBefore) != "" {
			builder.WriteString(` AND EXISTS (
				SELECT 1 FROM conversation x
				WHERE x.id = ?
				  AND (c.created_at < x.created_at OR (c.created_at = x.created_at AND c.id < x.id))
			)`)
			args = append(args, strings.TrimSpace(input.CursorBefore))
		}
		if input.Has.CursorAfter && strings.TrimSpace(input.CursorAfter) != "" {
			builder.WriteString(` AND EXISTS (
				SELECT 1 FROM conversation x
				WHERE x.id = ?
				  AND (c.created_at > x.created_at OR (c.created_at = x.created_at AND c.id > x.id))
			)`)
			args = append(args, strings.TrimSpace(input.CursorAfter))
		}
	}

	builder.WriteString(" ORDER BY COALESCE(c.last_activity, c.updated_at, c.created_at) DESC, c.id DESC")
	if limit > 0 {
		builder.WriteString(" LIMIT ?")
		args = append(args, limit)
	}
	return builder.String(), args
}

func scanConversationRowsView(rows *sql.Rows) (*agconvlist.ConversationRowsView, error) {
	result := &agconvlist.ConversationRowsView{}
	var (
		lastTurnID               sql.NullString
		summary                  sql.NullString
		lastActivity             sql.NullTime
		usageInputTokens         sql.NullInt64
		usageOutputTokens        sql.NullInt64
		usageEmbeddingTokens     sql.NullInt64
		updatedAt                sql.NullTime
		createdByUserID          sql.NullString
		agentID                  sql.NullString
		defaultModelProvider     sql.NullString
		defaultModel             sql.NullString
		defaultModelParams       sql.NullString
		title                    sql.NullString
		conversationParentID     sql.NullString
		conversationParentTurnID sql.NullString
		metadata                 sql.NullString
		status                   sql.NullString
		scheduled                sql.NullInt64
		scheduleID               sql.NullString
		scheduleRunID            sql.NullString
		scheduleKind             sql.NullString
		scheduleTimezone         sql.NullString
		scheduleCronExpr         sql.NullString
		externalTaskRef          sql.NullString
	)
	if err := rows.Scan(
		&lastTurnID,
		&result.Stage,
		&result.Id,
		&summary,
		&lastActivity,
		&usageInputTokens,
		&usageOutputTokens,
		&usageEmbeddingTokens,
		&result.CreatedAt,
		&updatedAt,
		&createdByUserID,
		&agentID,
		&defaultModelProvider,
		&defaultModel,
		&defaultModelParams,
		&title,
		&conversationParentID,
		&conversationParentTurnID,
		&metadata,
		&result.Visibility,
		&result.Shareable,
		&status,
		&scheduled,
		&scheduleID,
		&scheduleRunID,
		&scheduleKind,
		&scheduleTimezone,
		&scheduleCronExpr,
		&externalTaskRef,
	); err != nil {
		return nil, err
	}
	result.LastTurnId = nullableStringPointer(lastTurnID)
	result.Summary = nullableStringPointer(summary)
	result.LastActivity = nullableTimePointer(lastActivity)
	result.UsageInputTokens = nullableIntPointer(usageInputTokens)
	result.UsageOutputTokens = nullableIntPointer(usageOutputTokens)
	result.UsageEmbeddingTokens = nullableIntPointer(usageEmbeddingTokens)
	result.UpdatedAt = nullableTimePointer(updatedAt)
	result.CreatedByUserId = nullableStringPointer(createdByUserID)
	result.AgentId = nullableStringPointer(agentID)
	result.DefaultModelProvider = nullableStringPointer(defaultModelProvider)
	result.DefaultModel = nullableStringPointer(defaultModel)
	result.DefaultModelParams = nullableStringPointer(defaultModelParams)
	result.Title = nullableStringPointer(title)
	result.ConversationParentId = nullableStringPointer(conversationParentID)
	result.ConversationParentTurnId = nullableStringPointer(conversationParentTurnID)
	result.Metadata = nullableStringPointer(metadata)
	result.Status = nullableStringPointer(status)
	result.Scheduled = nullableIntPointer(scheduled)
	result.ScheduleId = nullableStringPointer(scheduleID)
	result.ScheduleRunId = nullableStringPointer(scheduleRunID)
	result.ScheduleKind = nullableStringPointer(scheduleKind)
	result.ScheduleTimezone = nullableStringPointer(scheduleTimezone)
	result.ScheduleCronExpr = nullableStringPointer(scheduleCronExpr)
	result.ExternalTaskRef = nullableStringPointer(externalTaskRef)
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

func nullableStringPointer(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	v := value.String
	return &v
}

func nullableIntPointer(value sql.NullInt64) *int {
	if !value.Valid {
		return nil
	}
	v, err := strconv.Atoi(strconv.FormatInt(value.Int64, 10))
	if err != nil {
		v = int(value.Int64)
	}
	return &v
}

func nullableTimePointer(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	v := value.Time
	return &v
}

func (s *datlyService) db() (*sql.DB, error) {
	if s == nil || s.dao == nil {
		return nil, fmt.Errorf("data service is not initialized")
	}
	conn, err := s.dao.Resource().Connector("agently")
	if err != nil {
		return nil, fmt.Errorf("lookup agently connector: %w", err)
	}
	db, err := conn.DB()
	if err != nil {
		return nil, fmt.Errorf("open agently connector db: %w", err)
	}
	return db, nil
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
