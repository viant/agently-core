package scheduler

import (
	"context"
	"strings"
	"time"

	iauth "github.com/viant/agently-core/internal/auth"
	schrun "github.com/viant/agently-core/pkg/agently/scheduler/run"
	schedulepkg "github.com/viant/agently-core/pkg/agently/scheduler/schedule"
	schedwrite "github.com/viant/agently-core/pkg/agently/scheduler/schedule/write"
)

func toPublicSchedule(row *schedulepkg.ScheduleView) *Schedule {
	if row == nil {
		return nil
	}
	return &Schedule{
		ID:              row.Id,
		Name:            row.Name,
		Description:     row.Description,
		CreatedByUserID: row.CreatedByUserId,
		Visibility:      row.Visibility,
		AgentRef:        row.AgentRef,
		ModelOverride:   row.ModelOverride,
		UserCredURL:     row.UserCredURL,
		Enabled:         row.Enabled,
		StartAt:         row.StartAt,
		EndAt:           row.EndAt,
		ScheduleType:    row.ScheduleType,
		CronExpr:        row.CronExpr,
		IntervalSeconds: row.IntervalSeconds,
		Timezone:        row.Timezone,
		TimeoutSeconds:  row.TimeoutSeconds,
		TaskPromptURI:   row.TaskPromptUri,
		TaskPrompt:      row.TaskPrompt,
		NextRunAt:       row.NextRunAt,
		LastRunAt:       row.LastRunAt,
		LastStatus:      row.LastStatus,
		LastError:       row.LastError,
		LeaseOwner:      row.LeaseOwner,
		LeaseUntil:      row.LeaseUntil,
		CreatedAt:       row.CreatedAt,
		UpdatedAt:       row.UpdatedAt,
	}
}

func toMutableSchedule(schedule *Schedule, isUpdate bool) *schedwrite.Schedule {
	now := time.Now().UTC()
	mut := &schedwrite.Schedule{}
	if id := strings.TrimSpace(schedule.ID); id != "" {
		mut.SetId(id)
	}
	if name := strings.TrimSpace(schedule.Name); name != "" {
		mut.SetName(name)
	}
	if schedule.Description != nil {
		mut.SetDescription(strings.TrimSpace(*schedule.Description))
	}
	if schedule.CreatedByUserID != nil && strings.TrimSpace(*schedule.CreatedByUserID) != "" {
		mut.SetCreatedByUserID(strings.TrimSpace(*schedule.CreatedByUserID))
	}
	if visibility := strings.TrimSpace(schedule.Visibility); visibility != "" {
		mut.SetVisibility(visibility)
	}
	if agentRef := strings.TrimSpace(schedule.AgentRef); agentRef != "" {
		mut.SetAgentRef(agentRef)
	}
	if schedule.ModelOverride != nil && strings.TrimSpace(*schedule.ModelOverride) != "" {
		mut.SetModelOverride(strings.TrimSpace(*schedule.ModelOverride))
	}
	if schedule.UserCredURL != nil && strings.TrimSpace(*schedule.UserCredURL) != "" {
		mut.SetUserCredURL(strings.TrimSpace(*schedule.UserCredURL))
	}
	if isUpdate || schedule.Enabled {
		mut.SetEnabled(schedule.Enabled)
	}
	if schedule.StartAt != nil && !schedule.StartAt.IsZero() {
		mut.SetStartAt(schedule.StartAt.UTC())
	}
	if schedule.EndAt != nil && !schedule.EndAt.IsZero() {
		mut.SetEndAt(schedule.EndAt.UTC())
	}
	if scheduleType := strings.TrimSpace(schedule.ScheduleType); scheduleType != "" {
		mut.SetScheduleType(scheduleType)
	}
	if schedule.CronExpr != nil && strings.TrimSpace(*schedule.CronExpr) != "" {
		mut.SetCronExpr(strings.TrimSpace(*schedule.CronExpr))
	}
	if schedule.IntervalSeconds != nil {
		mut.SetIntervalSeconds(*schedule.IntervalSeconds)
	}
	if timezone := strings.TrimSpace(schedule.Timezone); timezone != "" {
		mut.SetTimezone(timezone)
	}
	if isUpdate || schedule.TimeoutSeconds > 0 {
		mut.SetTimeoutSeconds(schedule.TimeoutSeconds)
	}
	if schedule.TaskPromptURI != nil && strings.TrimSpace(*schedule.TaskPromptURI) != "" {
		mut.SetTaskPromptUri(strings.TrimSpace(*schedule.TaskPromptURI))
	}
	if schedule.TaskPrompt != nil && strings.TrimSpace(*schedule.TaskPrompt) != "" {
		mut.SetTaskPrompt(strings.TrimSpace(*schedule.TaskPrompt))
	}
	if schedule.NextRunAt != nil && !schedule.NextRunAt.IsZero() {
		mut.SetNextRunAt(schedule.NextRunAt.UTC())
	}
	if schedule.LastRunAt != nil && !schedule.LastRunAt.IsZero() {
		mut.SetLastRunAt(schedule.LastRunAt.UTC())
	}
	if schedule.LastStatus != nil && strings.TrimSpace(*schedule.LastStatus) != "" {
		mut.SetLastStatus(strings.TrimSpace(*schedule.LastStatus))
	}
	if schedule.LastError != nil && strings.TrimSpace(*schedule.LastError) != "" {
		mut.SetLastError(strings.TrimSpace(*schedule.LastError))
	}
	if schedule.LeaseOwner != nil && strings.TrimSpace(*schedule.LeaseOwner) != "" {
		mut.SetLeaseOwner(strings.TrimSpace(*schedule.LeaseOwner))
	}
	if schedule.LeaseUntil != nil && !schedule.LeaseUntil.IsZero() {
		mut.SetLeaseUntil(schedule.LeaseUntil.UTC())
	}
	if !schedule.CreatedAt.IsZero() {
		mut.SetCreatedAt(schedule.CreatedAt.UTC())
	}
	mut.SetUpdatedAt(now)
	return mut
}

func schedulePrompt(row *schedulepkg.ScheduleView) string {
	if row == nil {
		return ""
	}
	if row.TaskPrompt != nil && strings.TrimSpace(*row.TaskPrompt) != "" {
		return strings.TrimSpace(*row.TaskPrompt)
	}
	if row.TaskPromptUri != nil {
		return strings.TrimSpace(*row.TaskPromptUri)
	}
	return ""
}

func scheduleUserID(ctx context.Context, row *schedulepkg.ScheduleView) string {
	userID := strings.TrimSpace(iauth.EffectiveUserID(ctx))
	if userID != "" {
		return userID
	}
	if row != nil && row.CreatedByUserId != nil {
		if owner := strings.TrimSpace(*row.CreatedByUserId); owner != "" {
			return owner
		}
	}
	return "system"
}

func hasActiveRun(runs []*schrun.RunView) bool {
	for _, run := range runs {
		if run != nil && (run.CompletedAt == nil || run.CompletedAt.IsZero()) {
			return true
		}
	}
	return false
}

func findCompletedRunForSlot(runs []*schrun.RunView, scheduledFor time.Time) *schrun.RunView {
	slot := scheduledFor.UTC()
	for _, run := range runs {
		if run == nil || run.ScheduledFor == nil || run.CompletedAt == nil || run.CompletedAt.IsZero() {
			continue
		}
		if run.ScheduledFor.UTC().Equal(slot) {
			return run
		}
	}
	return nil
}

func valueOrEmpty(v *string) string {
	if v == nil {
		return ""
	}
	return strings.TrimSpace(*v)
}

func timePtrUTC(t time.Time) *time.Time {
	u := t.UTC()
	return &u
}
