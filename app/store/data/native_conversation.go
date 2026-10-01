package data

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	authctx "github.com/viant/agently-core/internal/auth"
	read "github.com/viant/agently-core/internal/datly/conversation/read"
	store "github.com/viant/agently-core/internal/store/conversation"
	agconv "github.com/viant/agently-core/pkg/agently/conversation"
	agconvlist "github.com/viant/agently-core/pkg/agently/conversation/list"
)

func nativeConversationInput(input *agconv.ConversationInput) *read.ConversationInput {
	query := &read.ConversationInput{}
	if input == nil || input.Has == nil {
		return query
	}
	h := input.Has
	if h.Since {
		query.SetSince(input.Since)
	}
	if h.IncludeTranscript {
		query.SetIncludeTranscript(input.IncludeTranscript)
	}
	if h.IncludeModelCal {
		query.SetIncludeModelCal(input.IncludeModelCal)
	}
	if h.IncludeToolCall {
		query.SetIncludeToolCall(input.IncludeToolCall)
	}
	if h.AgentId {
		query.SetAgentId(input.AgentId)
	}
	if h.ParentId {
		query.SetParentId(input.ParentId)
	}
	if h.ParentTurnId {
		query.SetParentTurnId(input.ParentTurnId)
	}
	if h.ExcludeChildren {
		query.SetExcludeChildren(input.ExcludeChildren)
	}
	if h.ExcludeScheduled {
		query.SetExcludeScheduled(input.ExcludeScheduled)
	}
	if h.ScheduleId {
		query.SetScheduleId(input.ScheduleId)
	}
	if h.ScheduleRunId {
		query.SetScheduleRunId(input.ScheduleRunId)
	}
	if h.Query {
		query.SetQuery(input.Query)
	}
	if h.StatusFilter {
		query.SetStatusFilter(input.StatusFilter)
	}
	if h.HasScheduleId {
		query.SetHasScheduleId(input.HasScheduleId)
	}
	return query
}

func mapNativeConversation(ctx context.Context, row *read.ConversationView) (*agconv.ConversationView, error) {
	if row == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(row)
	if err != nil {
		return nil, fmt.Errorf("encode native conversation: %w", err)
	}
	var mapped agconv.ConversationView
	if err := json.Unmarshal(encoded, &mapped); err != nil {
		return nil, fmt.Errorf("decode conversation contract: %w", err)
	}
	mapped.OnRelation(ctx)
	return &mapped, nil
}

func (s *datlyService) getConversationNative(ctx context.Context, id string, input *agconv.ConversationInput, opts *options) (*agconv.ConversationView, error) {
	component := &store.Store{Invoker: s.native, OwnerID: authctx.EffectiveUserID}
	query := nativeConversationInput(input)
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
	mapped, err := mapNativeConversation(ctx, row)
	if err != nil {
		return nil, err
	}
	if err := authorizeConversation(mapped, opts); err != nil {
		return nil, err
	}
	return mapped, nil
}

func (s *datlyService) loadConversationForAuthNative(ctx context.Context, id string) (*agconv.ConversationView, error) {
	component := &store.Store{Invoker: s.native, OwnerID: authctx.EffectiveUserID}
	row, err := component.GetBaseInternal(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrPermissionDenied
	}
	if err != nil {
		return nil, err
	}
	return mapNativeConversation(ctx, row)
}

func nativeConversationListInput(input *agconvlist.ConversationRowsInput) *read.ConversationInput {
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

func (s *datlyService) queryConversationRowsNative(ctx context.Context, input *agconvlist.ConversationRowsInput, limit int, direction Direction, callOpts *options) ([]*agconvlist.ConversationRowsView, error) {
	enforceVisibility := callOpts != nil && strings.TrimSpace(callOpts.principal) != "" && !callOpts.isAdmin
	component := &store.Store{Invoker: s.native, OwnerID: authctx.EffectiveUserID}
	rows, err := component.ListPage(ctx, nativeConversationListInput(input), limit, direction == DirectionAfter, enforceVisibility)
	if err != nil {
		return nil, err
	}
	result := make([]*agconvlist.ConversationRowsView, 0, len(rows))
	for _, row := range rows {
		if row == nil {
			continue
		}
		encoded, err := json.Marshal(row)
		if err != nil {
			return nil, fmt.Errorf("encode native conversation row: %w", err)
		}
		var mapped agconvlist.ConversationRowsView
		if err := json.Unmarshal(encoded, &mapped); err != nil {
			return nil, fmt.Errorf("decode conversation row contract: %w", err)
		}
		result = append(result, &mapped)
	}
	return result, nil
}
