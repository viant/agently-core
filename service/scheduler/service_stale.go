package scheduler

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	agrunwrite "github.com/viant/agently-core/pkg/agently/run/write"
	schrun "github.com/viant/agently-core/pkg/agently/scheduler/run"
	schedulepkg "github.com/viant/agently-core/pkg/agently/scheduler/schedule"
	schedwrite "github.com/viant/agently-core/pkg/agently/scheduler/schedule/write"
)

func (s *Service) isDue(ctx context.Context, row *schedulepkg.ScheduleView, now time.Time) (bool, time.Time, error) {
	if row == nil {
		return false, now, nil
	}
	if row.StartAt != nil && now.Before(row.StartAt.UTC()) {
		return false, now, nil
	}
	if row.EndAt != nil && !now.Before(row.EndAt.UTC()) {
		return false, now, nil
	}
	if strings.EqualFold(strings.TrimSpace(row.ScheduleType), "cron") && row.CronExpr != nil && strings.TrimSpace(*row.CronExpr) != "" {
		loc, _ := time.LoadLocation(strings.TrimSpace(row.Timezone))
		if loc == nil {
			loc = time.UTC
		}
		spec, err := parseCron(strings.TrimSpace(*row.CronExpr))
		if err != nil {
			return false, now, fmt.Errorf("invalid cron expr for schedule %s: %w", row.Id, err)
		}
		base := now.In(loc)
		if row.LastRunAt != nil {
			base = row.LastRunAt.In(loc)
		} else if !row.CreatedAt.IsZero() {
			base = row.CreatedAt.In(loc)
		}
		computedNext := cronNext(spec, base).UTC()
		if row.NextRunAt == nil || row.NextRunAt.IsZero() {
			if now.Before(computedNext) {
				mut := &schedwrite.Schedule{}
				mut.SetId(row.Id)
				mut.SetNextRunAt(computedNext)
				if err := s.store.PatchSchedule(ctx, mut); err != nil {
					return false, now, err
				}
			}
			return !now.Before(computedNext), computedNext, nil
		}
		return !now.Before(row.NextRunAt.UTC()), row.NextRunAt.UTC(), nil
	}
	if row.NextRunAt != nil && !row.NextRunAt.IsZero() {
		return !now.Before(row.NextRunAt.UTC()), row.NextRunAt.UTC(), nil
	}
	if row.IntervalSeconds != nil {
		base := row.CreatedAt.UTC()
		if row.LastRunAt != nil {
			base = row.LastRunAt.UTC()
		}
		next := base.Add(time.Duration(*row.IntervalSeconds) * time.Second)
		return !now.Before(next), next, nil
	}
	return false, now, nil
}

func (s *Service) cleanupStaleRuns(ctx context.Context, row *schedulepkg.ScheduleView, runs []*schrun.RunView, now time.Time) ([]*schrun.RunView, error) {
	if len(runs) == 0 {
		return runs, nil
	}
	result := make([]*schrun.RunView, 0, len(runs))
	for _, run := range runs {
		if run == nil {
			continue
		}
		if reason, stale := s.stalePendingRunReason(row, run, now); stale {
			if err := s.failStaleRun(ctx, row, run, reason, now); err != nil {
				return nil, err
			}
			result = append(result, failedRunCopy(run, reason, now))
			continue
		}
		if reason, stale := s.staleActiveRunReason(row, run, now); stale {
			if err := s.failStaleRun(ctx, row, run, reason, now); err != nil {
				return nil, err
			}
			result = append(result, failedRunCopy(run, reason, now))
			continue
		}
		result = append(result, run)
	}
	return result, nil
}

func (s *Service) stalePendingRunReason(row *schedulepkg.ScheduleView, run *schrun.RunView, now time.Time) (string, bool) {
	if run == nil || !strings.EqualFold(strings.TrimSpace(run.Status), "pending") || (run.CompletedAt != nil && !run.CompletedAt.IsZero()) {
		return "", false
	}
	if run.StartedAt != nil && !run.StartedAt.IsZero() {
		return "", false
	}
	if strings.TrimSpace(valueOrEmpty(run.ConversationId)) != "" || run.CreatedAt.IsZero() {
		return "", false
	}
	if run.ScheduledFor != nil && !run.ScheduledFor.IsZero() && !run.ScheduledFor.UTC().Before(now) {
		return "", false
	}
	staleAfter := defaultRunStartTimeout
	if row != nil && row.TimeoutSeconds > 0 {
		staleAfter = time.Duration(row.TimeoutSeconds) * time.Second
	}
	if staleAfter < 3*time.Minute {
		staleAfter = 3 * time.Minute
	}
	leaseWindow := s.leaseTTL * 3
	if leaseWindow > staleAfter {
		staleAfter = leaseWindow
	}
	staleAfter += staleRunGrace
	age := now.Sub(run.CreatedAt.UTC())
	if age < staleAfter {
		return "", false
	}
	return fmt.Sprintf("stale pending run detected: pending without conversation/start for %s", age.Round(time.Second)), true
}

func (s *Service) staleActiveRunReason(row *schedulepkg.ScheduleView, run *schrun.RunView, now time.Time) (string, bool) {
	runStart, timeout, stale := s.isStaleRun(row, run, now)
	if !stale {
		return "", false
	}
	return fmt.Sprintf("stale scheduled run detected: status=%s run_start=%s timeout=%s", strings.TrimSpace(run.Status), runStart.UTC().Format(time.RFC3339Nano), timeout), true
}

func (s *Service) isStaleRun(row *schedulepkg.ScheduleView, run *schrun.RunView, now time.Time) (time.Time, time.Duration, bool) {
	if run == nil || (run.CompletedAt != nil && !run.CompletedAt.IsZero()) {
		return time.Time{}, 0, false
	}
	runStart := run.CreatedAt.UTC()
	if run.StartedAt != nil && !run.StartedAt.IsZero() {
		runStart = run.StartedAt.UTC()
	}
	if runStart.IsZero() {
		return time.Time{}, 0, false
	}
	timeout := defaultStaleRunTimeout
	if row != nil && row.TimeoutSeconds > 0 {
		timeout = time.Duration(row.TimeoutSeconds) * time.Second
	}
	if run.LeaseUntil != nil && !run.LeaseUntil.IsZero() {
		return runStart, timeout, now.After(run.LeaseUntil.UTC().Add(staleRunGrace))
	}
	return runStart, timeout, now.After(runStart.Add(timeout).Add(staleRunGrace))
}

func failedRunCopy(run *schrun.RunView, reason string, now time.Time) *schrun.RunView {
	if run == nil {
		return nil
	}
	copyValue := *run
	copyValue.Status = "failed"
	copyValue.CompletedAt = timePtrUTC(now)
	if strings.TrimSpace(reason) != "" {
		msg := strings.TrimSpace(reason)
		copyValue.ErrorMessage = &msg
	}
	return &copyValue
}

func (s *Service) failStaleRun(ctx context.Context, row *schedulepkg.ScheduleView, run *schrun.RunView, reason string, now time.Time) error {
	if run == nil {
		return nil
	}
	patch := &agrunwrite.MutableRunView{}
	patch.SetId(run.Id)
	patch.SetStatus("failed")
	patch.SetCompletedAt(now)
	if strings.TrimSpace(reason) != "" {
		patch.SetErrorMessage(reason)
	}
	if err := s.store.PatchRuns(ctx, []*agrunwrite.MutableRunView{patch}); err != nil {
		return fmt.Errorf("patch stale run %s: %w", run.Id, err)
	}
	conversationID := strings.TrimSpace(valueOrEmpty(run.ConversationId))
	if conversationID != "" {
		if err := s.cancelConversationAndMark(ctx, conversationID, "canceled"); err != nil {
			log.Printf("scheduler: cancel stale conversation schedule=%s run=%s conversation=%s: %v", row.Id, run.Id, conversationID, err)
		}
	}
	if row != nil {
		lastRunAt := now.UTC()
		if run.CompletedAt != nil && !run.CompletedAt.IsZero() {
			lastRunAt = run.CompletedAt.UTC()
		}
		_ = s.patchScheduleResult(ctx, row, "failed", reason, &lastRunAt, false, now)
	}
	return nil
}
