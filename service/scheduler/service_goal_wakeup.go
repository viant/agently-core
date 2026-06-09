package scheduler

import (
	"context"
	"strings"
	"time"

	schedulepkg "github.com/viant/agently-core/pkg/agently/scheduler/schedule"
	schedwrite "github.com/viant/agently-core/pkg/agently/scheduler/schedule/write"
	agentsvc "github.com/viant/agently-core/service/agent"
	"github.com/viant/agently-core/workspace"
	wscfg "github.com/viant/agently-core/workspace/config"
)

type GoalControllerSchedule struct {
	Mode    string
	Preview *string
	WakeAt  *time.Time
}

func (s *Service) ScheduleGoalWakeup(ctx context.Context, req agentsvc.GoalWakeupRequest) (bool, error) {
	if s == nil || s.store == nil {
		return false, nil
	}
	conversationID := strings.TrimSpace(req.ConversationID)
	goalID := strings.TrimSpace(req.GoalID)
	agentID := strings.TrimSpace(req.AgentID)
	if conversationID == "" || goalID == "" || agentID == "" || req.WakeAt.IsZero() {
		return false, nil
	}
	if err := s.CancelGoalWakeups(ctx, conversationID, goalID); err != nil {
		return false, err
	}
	allowed, err := s.withinGoalWakeupBudget(ctx, req)
	if err != nil {
		return false, err
	}
	if !allowed {
		return false, nil
	}
	name := "autonomous::goal-wakeup::" + goalID
	row := &Schedule{
		ID:              "goal-wakeup-" + goalID,
		Name:            name,
		CreatedByUserID: stringPtr(strings.TrimSpace(req.UserID)),
		Visibility:      "private",
		Internal:        true,
		ConversationID:  stringPtr(conversationID),
		GoalID:          stringPtr(goalID),
		AgentRef:        agentID,
		ModelOverride:   stringPtr(strings.TrimSpace(req.ModelOverride)),
		Enabled:         true,
		ScheduleType:    "adhoc",
		Timezone:        "UTC",
		TaskPrompt:      stringPtr(strings.TrimSpace(req.Payload)),
		Description:     stringPtr(strings.TrimSpace(req.Preview)),
		NextRunAt:       timePtrUTC(req.WakeAt),
	}
	if err := s.store.PatchSchedule(ctx, toMutableSchedule(row, false)); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Service) CancelGoalWakeups(ctx context.Context, conversationID, goalID string) error {
	if s == nil || s.store == nil {
		return nil
	}
	conversationID = strings.TrimSpace(conversationID)
	goalID = strings.TrimSpace(goalID)
	rows, err := s.store.ListForRunDue(ctx)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row == nil || !row.Internal {
			continue
		}
		if conversationID != "" && strings.TrimSpace(valueOrEmpty(row.ConversationId)) != conversationID {
			continue
		}
		if goalID != "" && strings.TrimSpace(valueOrEmpty(row.GoalId)) != goalID {
			continue
		}
		mut := &schedwrite.Schedule{}
		mut.SetId(strings.TrimSpace(row.Id))
		mut.SetEnabled(false)
		mut.NextRunAt = nil
		mut.Has.NextRunAt = true
		cancelStatus := "canceled"
		mut.SetLastStatus(cancelStatus)
		cancelReason := "autonomous wakeup canceled"
		mut.SetLastError(cancelReason)
		mut.SetUpdatedAt(time.Now().UTC())
		if err := s.store.PatchSchedule(ctx, mut); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) CurrentGoalWakeup(ctx context.Context, conversationID, goalID string) *GoalControllerSchedule {
	if s == nil || s.store == nil {
		return nil
	}
	conversationID = strings.TrimSpace(conversationID)
	goalID = strings.TrimSpace(goalID)
	if conversationID == "" || goalID == "" {
		return nil
	}
	rows, err := s.store.ListForRunDue(ctx)
	if err != nil {
		return nil
	}
	now := time.Now().UTC()
	var selected *schedulepkg.ScheduleView
	for _, row := range rows {
		if row == nil || !row.Internal || !row.Enabled || row.NextRunAt == nil {
			continue
		}
		if strings.TrimSpace(valueOrEmpty(row.ConversationId)) != conversationID {
			continue
		}
		if strings.TrimSpace(valueOrEmpty(row.GoalId)) != goalID {
			continue
		}
		if row.NextRunAt.UTC().Before(now) {
			continue
		}
		if selected == nil || row.NextRunAt.UTC().Before(selected.NextRunAt.UTC()) {
			selected = row
		}
	}
	if selected == nil {
		return nil
	}
	return &GoalControllerSchedule{
		Mode:    "wakeup",
		Preview: stringPtr(strings.TrimSpace(valueOrEmpty(selected.Description))),
		WakeAt:  timePtrUTC(selected.NextRunAt.UTC()),
	}
}

func stringPtr(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	copied := value
	return &copied
}

func (s *Service) withinGoalWakeupBudget(ctx context.Context, req agentsvc.GoalWakeupRequest) (bool, error) {
	cfg, err := wscfg.Load(workspace.Root())
	if err == nil && cfg != nil && !cfg.GoalWakeupsEnabled() {
		return false, nil
	}
	rows, err := s.store.ListForRunDue(ctx)
	if err != nil {
		return false, err
	}
	now := time.Now().UTC()
	globalLimit := 5
	conversationLimit := 3
	goalLimit := 2
	if cfg != nil {
		globalLimit = cfg.GoalWakeupMaxGlobalWakeupsPerHour()
		conversationLimit = cfg.GoalWakeupMaxConversationWakeups()
		goalLimit = cfg.GoalWakeupMaxGoalWakeups()
	}
	globalCount := 0
	conversationCount := 0
	goalCount := 0
	horizon := now.Add(time.Hour)
	for _, row := range rows {
		if row == nil || !row.Internal || !row.Enabled || row.NextRunAt == nil {
			continue
		}
		wakeAt := row.NextRunAt.UTC()
		if wakeAt.Before(now) {
			continue
		}
		if wakeAt.Before(horizon) || wakeAt.Equal(horizon) {
			globalCount++
		}
		if strings.TrimSpace(valueOrEmpty(row.ConversationId)) == strings.TrimSpace(req.ConversationID) {
			conversationCount++
		}
		if strings.TrimSpace(valueOrEmpty(row.GoalId)) == strings.TrimSpace(req.GoalID) {
			goalCount++
		}
	}
	if globalLimit > 0 && globalCount >= globalLimit {
		return false, nil
	}
	if conversationLimit > 0 && conversationCount >= conversationLimit {
		return false, nil
	}
	if goalLimit > 0 && goalCount >= goalLimit {
		return false, nil
	}
	return true, nil
}
