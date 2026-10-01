package scheduler

import (
	"context"
	"encoding/json"
	"fmt"

	authctx "github.com/viant/agently-core/internal/auth"
	write "github.com/viant/agently-core/internal/datly/schedule/write"
	store "github.com/viant/agently-core/internal/store/schedulerstore"
	legacy "github.com/viant/agently-core/pkg/agently/scheduler/schedule"
	legacywrite "github.com/viant/agently-core/pkg/agently/scheduler/schedule/write"
)

func (s *datlyStore) nativeScheduleStore() *store.Store {
	return &store.Store{Invoker: s.native, OwnerID: authctx.EffectiveUserID}
}

func (s *datlyStore) listSchedulesNative(ctx context.Context, id string, internal bool) ([]*legacy.ScheduleView, error) {
	rows, err := s.nativeScheduleStore().List(ctx, id, internal)
	if err != nil {
		return nil, err
	}
	result := make([]*legacy.ScheduleView, 0, len(rows))
	for _, row := range rows {
		if row == nil {
			continue
		}
		encoded, err := json.Marshal(row)
		if err != nil {
			return nil, fmt.Errorf("encode native schedule: %w", err)
		}
		var mapped legacy.ScheduleView
		if err := json.Unmarshal(encoded, &mapped); err != nil {
			return nil, fmt.Errorf("decode schedule contract: %w", err)
		}
		result = append(result, &mapped)
	}
	return result, nil
}

func (s *datlyStore) patchScheduleNative(ctx context.Context, schedule *legacywrite.Schedule) error {
	encoded, err := json.Marshal(schedule)
	if err != nil {
		return fmt.Errorf("encode schedule mutation: %w", err)
	}
	var row write.Schedule
	if err := json.Unmarshal(encoded, &row); err != nil {
		return fmt.Errorf("decode native schedule mutation: %w", err)
	}
	if h := schedule.Has; h != nil {
		row.Has = &write.ScheduleHas{
			Id: h.Id, Name: h.Name, Description: h.Description,
			CreatedByUserId: h.CreatedByUserID, Visibility: h.Visibility, Internal: h.Internal,
			ConversationId: h.ConversationID, GoalId: h.GoalID, AgentRef: h.AgentRef,
			ModelOverride: h.ModelOverride, UserCredUrl: h.UserCredURL, Enabled: h.Enabled,
			StartAt: h.StartAt, EndAt: h.EndAt, ScheduleType: h.ScheduleType,
			CronExpr: h.CronExpr, IntervalSeconds: h.IntervalSeconds,
			Timezone: h.Timezone, TimeoutSeconds: h.TimeoutSeconds,
			TaskPromptUri: h.TaskPromptUri, TaskPrompt: h.TaskPrompt,
			NextRunAt: h.NextRunAt, LastRunAt: h.LastRunAt,
			LastStatus: h.LastStatus, LastError: h.LastError,
			LeaseOwner: h.LeaseOwner, LeaseUntil: h.LeaseUntil,
			CreatedAt: h.CreatedAt, UpdatedAt: h.UpdatedAt,
		}
	}
	return s.nativeScheduleStore().PatchTrusted(ctx, &row)
}
