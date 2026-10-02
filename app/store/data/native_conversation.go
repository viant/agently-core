package data

import (
	"context"
	"errors"
	"strings"

	authctx "github.com/viant/agently-core/internal/auth"
	read "github.com/viant/agently-core/internal/datly/conversation/read"
	store "github.com/viant/agently-core/internal/store/conversation"
	conversationmodel "github.com/viant/agently-core/model/conversation"
)

func nativeConversationInput(input *conversationmodel.ConversationInput) (*read.ConversationInput, error) {
	if input == nil || input.Has == nil {
		return &read.ConversationInput{}, nil
	}
	return mapDataDTO[read.ConversationInput](input)
}

func normalizeNativeConversationGet(ctx context.Context, row *read.ConversationView) *conversationmodel.ConversationView {
	if row == nil {
		return nil
	}
	// Base fallback rows are read in list mode to avoid loading the graph.
	// The public get still applies its existing relation/stage normalization.
	row.ListMode = false
	row.OnRelation(ctx)
	return row
}

func (s *datlyService) getConversationNative(ctx context.Context, id string, input *conversationmodel.ConversationInput, opts *options) (*conversationmodel.ConversationView, error) {
	component := &store.Store{Invoker: s.native, OwnerID: authctx.EffectiveUserID}
	query, err := nativeConversationInput(input)
	if err != nil {
		return nil, err
	}
	selectors := nativeSelectors(nil)
	if opts != nil {
		selectors = nativeSelectors(opts.selectors)
	}
	row, err := component.GetInternal(ctx, strings.TrimSpace(id), query, selectors)
	if errors.Is(err, store.ErrNotFound) && input != nil && (input.IncludeTranscript || input.IncludeModelCal || input.IncludeToolCall) {
		row, err = component.GetBaseInternal(ctx, id)
	}
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	mapped := normalizeNativeConversationGet(ctx, row)
	if err := authorizeConversation(mapped, opts); err != nil {
		return nil, err
	}
	return mapped, nil
}

func (s *datlyService) loadConversationForAuthNative(ctx context.Context, id string) (*conversationmodel.ConversationView, error) {
	component := &store.Store{Invoker: s.native, OwnerID: authctx.EffectiveUserID}
	row, err := component.GetBaseInternal(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrPermissionDenied
	}
	if err != nil {
		return nil, err
	}
	return normalizeNativeConversationGet(ctx, row), nil
}

func nativeConversationListInput(input *conversationmodel.ConversationRowsInput) *read.ConversationInput {
	query := &read.ConversationInput{}
	if input == nil || input.Has == nil {
		return query
	}
	h := input.Has
	if h.AgentId && strings.TrimSpace(input.AgentId) != "" {
		query.SetAgentId(strings.TrimSpace(input.AgentId))
	}
	if h.ParentId && strings.TrimSpace(input.ParentId) != "" {
		query.SetParentId(strings.TrimSpace(input.ParentId))
	}
	if h.ParentTurnId && strings.TrimSpace(input.ParentTurnId) != "" {
		query.SetParentTurnId(strings.TrimSpace(input.ParentTurnId))
	}
	if h.ExcludeChildren {
		query.SetExcludeChildren(input.ExcludeChildren)
	}
	if h.ExcludeScheduled && input.ExcludeScheduled {
		query.SetExcludeScheduled(true)
	}
	if h.ScheduleId && strings.TrimSpace(input.ScheduleId) != "" {
		query.SetScheduleId(strings.TrimSpace(input.ScheduleId))
	}
	if h.ScheduleRunId && strings.TrimSpace(input.ScheduleRunId) != "" {
		query.SetScheduleRunId(strings.TrimSpace(input.ScheduleRunId))
	}
	if h.Query && strings.TrimSpace(input.Query) != "" {
		query.SetQuery(strings.TrimSpace(input.Query))
	}
	if h.StatusFilter && strings.TrimSpace(input.StatusFilter) != "" {
		query.SetStatusFilter(strings.TrimSpace(input.StatusFilter))
	}
	if h.CreatedSince && !input.CreatedSince.IsZero() {
		query.SetCreatedSince(input.CreatedSince)
	}
	if h.CreatedBefore && !input.CreatedBefore.IsZero() {
		query.SetCreatedBefore(input.CreatedBefore)
	}
	if h.CursorBefore && strings.TrimSpace(input.CursorBefore) != "" {
		query.SetCursorBefore(strings.TrimSpace(input.CursorBefore))
	}
	if h.CursorAfter && strings.TrimSpace(input.CursorAfter) != "" {
		query.SetCursorAfter(strings.TrimSpace(input.CursorAfter))
	}
	return query
}

func (s *datlyService) queryConversationRowsNative(ctx context.Context, input *conversationmodel.ConversationRowsInput, limit int, direction Direction, callOpts *options) ([]*conversationmodel.ConversationRowsView, error) {
	enforceVisibility := callOpts != nil && strings.TrimSpace(callOpts.principal) != "" && !callOpts.isAdmin
	component := &store.Store{Invoker: s.native, OwnerID: authctx.EffectiveUserID}
	rows, err := component.ListPage(ctx, nativeConversationListInput(input), limit, direction == DirectionAfter, enforceVisibility)
	if err != nil {
		return nil, err
	}
	return rows, nil
}
