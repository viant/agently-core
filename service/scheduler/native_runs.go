package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/viant/agently-core/app/store/data"
	authctx "github.com/viant/agently-core/internal/auth"
	cube "github.com/viant/agently-core/internal/datly/run/cube"
	read "github.com/viant/agently-core/internal/datly/run/read"
	store "github.com/viant/agently-core/internal/store/agentrun"
	legacy "github.com/viant/agently-core/pkg/agently/scheduler/run"
	"github.com/viant/xdatly/state"
)

func (s *datlyStore) nativeRunStore() *store.Store {
	return &store.Store{Invoker: s.native, OwnerID: authctx.EffectiveUserID}
}

func mapSchedulerRun(row *read.RunRowsView) (*legacy.RunView, error) {
	if row == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(row)
	if err != nil {
		return nil, fmt.Errorf("encode native scheduler run: %w", err)
	}
	var mapped legacy.RunView
	if err := json.Unmarshal(encoded, &mapped); err != nil {
		return nil, fmt.Errorf("decode scheduler run contract: %w", err)
	}
	return &mapped, nil
}

func (s *datlyStore) listRunsNative(ctx context.Context, in *legacy.RunListInput, page, size int) (*RunListPage, error) {
	if in != nil && strings.TrimSpace(in.EffectiveUserID) != "" && !strings.EqualFold(strings.TrimSpace(in.EffectiveUserID), strings.TrimSpace(authctx.EffectiveUserID(ctx))) {
		return nil, data.ErrPermissionDenied
	}
	if page < 1 {
		page = 1
	}
	if size <= 0 {
		size = 10
	}
	listInput, totalInput := cloneRunInputs(ctx, in, page, size)
	query := &read.RunRowsInput{}
	countQuery := &cube.RunReportInput{}
	if listInput.Has.Since {
		query.SetSinceTurn(listInput.Since)
		countQuery.SetSinceTurn(totalInput.Since)
	}
	if listInput.Has.ScheduleId {
		query.SetScheduleId(listInput.ScheduleId)
		countQuery.SetScheduleId(totalInput.ScheduleId)
	}
	if listInput.Has.RunStatus {
		query.SetStatusPattern(listInput.RunStatus)
		countQuery.SetStatusPattern(totalInput.RunStatus)
	}
	if listInput.Has.ConversationId {
		query.SetConversationPattern(listInput.ConversationId)
		countQuery.SetConversationPattern(totalInput.ConversationId)
	}
	if listInput.Has.ErrorMessage {
		query.SetErrorPattern(listInput.ErrorMessage)
		countQuery.SetErrorPattern(totalInput.ErrorMessage)
	}
	selectors := state.Selectors{&state.NamedSelector{Name: "reader", Selector: state.Selector{
		Fields: store.SchedulerRunFields(), Limit: size, Offset: (page - 1) * size,
	}}}
	component := s.nativeRunStore()
	rows, err := component.ListTrusted(ctx, "schedulerList", query, selectors)
	if err != nil {
		return nil, err
	}
	total, err := component.CountScheduler(ctx, countQuery)
	if err != nil {
		return nil, err
	}
	result := &RunListPage{Rows: make([]*legacy.RunView, 0, len(rows)), TotalCount: total, PageCount: computePageCount(total, size)}
	for _, row := range rows {
		mapped, err := mapSchedulerRun(row)
		if err != nil {
			return nil, err
		}
		if mapped != nil {
			result.Rows = append(result.Rows, mapped)
		}
	}
	return result, nil
}

func (s *datlyStore) listRunsForDueNative(ctx context.Context, scheduleID string, scheduledFor *time.Time, excludeStatuses []string) ([]*legacy.RunView, error) {
	query := &read.RunRowsInput{}
	query.SetScheduleId(strings.TrimSpace(scheduleID))
	if scheduledFor != nil && !scheduledFor.IsZero() {
		query.SetScheduledFor(scheduledFor.UTC())
	}
	if len(excludeStatuses) > 0 {
		query.SetExcludeStatuses(append([]string(nil), excludeStatuses...))
	}
	rows, err := s.nativeRunStore().ListTrusted(ctx, "schedulerDue", query, nil)
	if err != nil {
		return nil, err
	}
	result := make([]*legacy.RunView, 0, len(rows))
	for _, row := range rows {
		mapped, err := mapSchedulerRun(row)
		if err != nil {
			return nil, err
		}
		if mapped != nil {
			result = append(result, mapped)
		}
	}
	return result, nil
}
