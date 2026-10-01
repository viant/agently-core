package data

import (
	"context"
	"encoding/json"
	"fmt"

	authctx "github.com/viant/agently-core/internal/auth"
	read "github.com/viant/agently-core/internal/datly/run/read"
	store "github.com/viant/agently-core/internal/store/agentrun"
	runmodel "github.com/viant/agently-core/model/run"
)

func nativeRunInput(id string, input *runmodel.RunRowsInput) *read.RunRowsInput {
	query := &read.RunRowsInput{}
	query.SetId(id)
	if input == nil || input.Has == nil {
		return query
	}
	h := input.Has
	if h.TurnId {
		query.SetTurnId(input.TurnId)
	}
	if h.ConversationId {
		query.SetConversationId(input.ConversationId)
	}
	if h.ScheduleId {
		query.SetScheduleId(input.ScheduleId)
	}
	if h.WorkerId {
		query.SetWorkerId(input.WorkerId)
	}
	if h.RunStatus {
		query.SetRunStatus(input.RunStatus)
	}
	if h.ExcludeStatuses {
		query.SetExcludeStatuses(input.ExcludeStatuses)
	}
	return query
}

func (s *datlyService) getRunNative(ctx context.Context, id string, input *runmodel.RunRowsInput, opts *options) (*runmodel.RunRowsView, error) {
	component := &store.Store{Invoker: s.native, OwnerID: authctx.EffectiveUserID}
	var selectors = nativeSelectors(nil)
	if opts != nil {
		selectors = nativeSelectors(opts.selectors)
	}
	row, err := component.GetTrusted(ctx, nativeRunInput(id, input), selectors)
	if err != nil || row == nil {
		return nil, err
	}
	encoded, err := json.Marshal(row)
	if err != nil {
		return nil, fmt.Errorf("encode native run: %w", err)
	}
	var mapped runmodel.RunRowsView
	if err := json.Unmarshal(encoded, &mapped); err != nil {
		return nil, fmt.Errorf("decode run contract: %w", err)
	}
	if mapped.ConversationId != nil {
		if err := s.authorizeConversationID(ctx, *mapped.ConversationId, opts, nil); err != nil {
			return nil, err
		}
	}
	return &mapped, nil
}

func mapRunRow[T any](row *read.RunRowsView) (*T, error) {
	if row == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(row)
	if err != nil {
		return nil, fmt.Errorf("encode native run row: %w", err)
	}
	var mapped T
	if err := json.Unmarshal(encoded, &mapped); err != nil {
		return nil, fmt.Errorf("decode run row contract: %w", err)
	}
	return &mapped, nil
}

func (s *datlyService) getActiveRunNative(ctx context.Context, input *runmodel.ActiveRunsInput, opts *options) (*runmodel.ActiveRunsView, error) {
	query := &read.RunRowsInput{}
	if input != nil && input.Has != nil {
		if input.Has.TurnId {
			query.SetTurnId(input.TurnId)
		}
		if input.Has.ConversationId {
			query.SetConversationId(input.ConversationId)
		}
	}
	component := &store.Store{Invoker: s.native, OwnerID: authctx.EffectiveUserID}
	rows, err := component.ListTrusted(ctx, "active", query, nativeSelectors(opts.selectors))
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	mapped, err := mapRunRow[runmodel.ActiveRunsView](rows[0])
	if err != nil {
		return nil, err
	}
	if mapped != nil && mapped.ConversationId != nil {
		if err := s.authorizeConversationID(ctx, *mapped.ConversationId, opts, nil); err != nil {
			return nil, err
		}
	}
	return mapped, nil
}

func (s *datlyService) listStaleRunsNative(ctx context.Context, input *runmodel.StaleRunsInput, opts *options) ([]*runmodel.StaleRunsView, error) {
	query := &read.RunRowsInput{}
	if input != nil && input.Has != nil {
		h := input.Has
		if h.HeartbeatBefore {
			query.SetHeartbeatBefore(input.HeartbeatBefore)
		}
		if h.WorkerHost {
			query.SetWorkerHost(input.WorkerHost)
		}
		if h.LeaseExpiredBefore {
			query.SetLeaseExpiredBefore(input.LeaseExpiredBefore)
		}
		if h.ActivityAfter {
			query.SetActivityAfter(input.ActivityAfter)
		}
		if h.ConversationKind {
			query.SetConversationKind(input.ConversationKind)
		}
		if h.RootInteractive {
			query.SetRootInteractive(input.RootInteractive)
		}
	}
	component := &store.Store{Invoker: s.native, OwnerID: authctx.EffectiveUserID}
	rows, err := component.ListTrusted(ctx, "stale", query, nativeSelectors(opts.selectors))
	if err != nil {
		return nil, err
	}
	result := make([]*runmodel.StaleRunsView, 0, len(rows))
	cache := newAuthCache()
	for _, row := range rows {
		mapped, err := mapRunRow[runmodel.StaleRunsView](row)
		if err != nil {
			return nil, err
		}
		if mapped == nil {
			continue
		}
		if opts != nil && opts.principal != "" && !opts.isAdmin {
			if mapped.ConversationId == nil {
				continue
			}
			if err := s.authorizeConversationID(ctx, *mapped.ConversationId, opts, cache); err != nil {
				continue
			}
		}
		result = append(result, mapped)
	}
	return result, nil
}
