package scheduler

import (
	"context"
	"encoding/json"
	"fmt"

	authctx "github.com/viant/agently-core/internal/auth"
	write "github.com/viant/agently-core/internal/datly/schedule/write"
	store "github.com/viant/agently-core/internal/store/schedulerstore"
	schedulemodel "github.com/viant/agently-core/model/schedule"
)

func (s *datlyStore) nativeScheduleStore() *store.Store {
	return &store.Store{Invoker: s.native, OwnerID: authctx.EffectiveUserID}
}

func (s *datlyStore) listSchedulesNative(ctx context.Context, id string, internal bool) ([]*schedulemodel.ScheduleView, error) {
	rows, err := s.nativeScheduleStore().List(ctx, id, internal)
	if err != nil {
		return nil, err
	}
	return rows, nil
}

func (s *datlyStore) patchScheduleNative(ctx context.Context, schedule *schedulemodel.Schedule) error {
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
