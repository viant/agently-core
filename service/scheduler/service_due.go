package scheduler

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
	convcli "github.com/viant/agently-core/app/store/conversation"
	agrunwrite "github.com/viant/agently-core/pkg/agently/run/write"
	schedulepkg "github.com/viant/agently-core/pkg/agently/scheduler/schedule"
)

// RunDue scans persisted schedules, claims due slots, and starts runs.
func (s *Service) RunDue(ctx context.Context) (int, error) {
	if s == nil || s.store == nil || s.agent == nil {
		return 0, fmt.Errorf("scheduler service not initialized")
	}
	s.ensureLeaseConfig()
	rows, err := s.store.ListForRunDue(ctx)
	if err != nil {
		return 0, err
	}
	started := 0
	for _, row := range rows {
		if row == nil {
			continue
		}
		now := time.Now().UTC()
		due := false
		scheduledFor := now
		if row.Enabled {
			due, scheduledFor, err = s.isDue(ctx, row, now)
			if err != nil {
				return started, err
			}
		}
		includeScheduledSlot := row.Enabled && due && row.NextRunAt != nil && !row.NextRunAt.IsZero()
		runs, err := s.getRunsForDueCheck(ctx, row.Id, scheduledFor, includeScheduledSlot)
		if err != nil {
			return started, err
		}
		if len(runs) == 0 && (!row.Enabled || !due) {
			continue
		}
		claimed, err := s.store.TryClaimSchedule(ctx, row.Id, s.leaseOwner, now.Add(s.leaseTTL))
		if err != nil {
			return started, err
		}
		if !claimed {
			continue
		}
		func() {
			defer func() { _, _ = s.store.ReleaseScheduleLease(context.Background(), row.Id, s.leaseOwner) }()
			runs, err = s.getRunsForDueCheck(ctx, row.Id, scheduledFor, includeScheduledSlot)
			if err != nil {
				return
			}
			runs, err = s.cleanupStaleRuns(ctx, row, runs, now)
			if err != nil {
				return
			}
			if !row.Enabled || !due {
				return
			}
			if terminal := findCompletedRunForSlot(runs, scheduledFor); terminal != nil {
				lastRunAt := scheduledFor
				if terminal.CompletedAt != nil && !terminal.CompletedAt.IsZero() {
					lastRunAt = terminal.CompletedAt.UTC()
				}
				_ = s.patchScheduleResult(ctx, row, terminal.Status, valueOrEmpty(terminal.ErrorMessage), &lastRunAt, true, now)
				return
			}
			if hasActiveRun(runs) {
				return
			}
			if runErr := s.enqueueAndLaunch(ctx, row, scheduledFor, true); runErr != nil {
				err = runErr
				return
			}
			started++
		}()
		if err != nil {
			return started, err
		}
	}
	return started, nil
}

// processDue is used by the watchdog ticker. It skips the tick entirely when
// another RunDue pass is still in flight so overlapping ticks cannot race on
// schedule claims or produce duplicate runs.
func (s *Service) processDue(ctx context.Context) {
	if !s.runDueMu.TryLock() {
		log.Printf("scheduler: processDue skipped — previous pass still running")
		return
	}
	defer s.runDueMu.Unlock()
	if _, err := s.RunDue(ctx); err != nil {
		log.Printf("scheduler: run due: %v", err)
	}
}

func (s *Service) enqueueAndLaunch(ctx context.Context, row *schedulepkg.ScheduleView, scheduledFor time.Time, advanceNext bool) error {
	if row == nil {
		return fmt.Errorf("schedule is required")
	}
	s.ensureLeaseConfig()
	runID := uuid.NewString()
	now := time.Now().UTC()
	userID := scheduleUserID(ctx, row)

	run := &agrunwrite.MutableRunView{}
	run.SetId(runID)
	run.SetScheduleID(row.Id)
	run.SetConversationKind("scheduled")
	run.SetStatus("pending")
	run.SetScheduledFor(scheduledFor.UTC())
	run.SetCreatedAt(now)
	run.SetAgentID(strings.TrimSpace(row.AgentRef))
	if userID != "" {
		run.SetEffectiveUserID(userID)
	}
	if cred := strings.TrimSpace(valueOrEmpty(row.UserCredURL)); cred != "" {
		run.SetUserCredURL(cred)
	}
	if err := s.store.PatchRuns(ctx, []*agrunwrite.MutableRunView{run}); err != nil {
		return err
	}
	if advanceNext {
		if err := s.patchScheduleResult(ctx, row, "", "", nil, true, now); err != nil {
			return err
		}
	}
	// Acquire a slot from the concurrency semaphore synchronously (so a
	// flood of due schedules cannot explode into thousands of goroutines).
	// When no cap is configured the channel is nil and this is a no-op.
	if s.execSem != nil {
		select {
		case s.execSem <- struct{}{}:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	go func() {
		if s.execSem != nil {
			defer func() { <-s.execSem }()
		}
		s.executeRun(context.WithoutCancel(ctx), row, runID, scheduledFor.UTC())
	}()
	return nil
}

func scheduleExecutionTimeout(row *schedulepkg.ScheduleView) time.Duration {
	if row != nil && row.TimeoutSeconds > 0 {
		return time.Duration(row.TimeoutSeconds) * time.Second
	}
	return defaultStaleRunTimeout
}

func (s *Service) annotateConversation(ctx context.Context, row *schedulepkg.ScheduleView, conversationID, runID string) {
	if s == nil || s.conversation == nil || strings.TrimSpace(conversationID) == "" || row == nil {
		return
	}
	conv := convcli.NewConversation()
	conv.SetId(strings.TrimSpace(conversationID))
	conv.SetScheduled(1)
	conv.SetScheduleId(strings.TrimSpace(row.Id))
	conv.SetScheduleRunId(strings.TrimSpace(runID))
	conv.SetScheduleKind(strings.TrimSpace(row.ScheduleType))
	if tz := strings.TrimSpace(row.Timezone); tz != "" {
		conv.SetScheduleTimezone(tz)
	}
	if row.CronExpr != nil && strings.TrimSpace(*row.CronExpr) != "" {
		conv.SetScheduleCronExpr(strings.TrimSpace(*row.CronExpr))
	}
	if owner := strings.TrimSpace(valueOrEmpty(row.CreatedByUserId)); owner != "" {
		conv.SetCreatedByUserID(owner)
	}
	if err := s.conversation.PatchConversations(ctx, conv); err != nil {
		log.Printf("scheduler: annotate conversation schedule=%s run=%s conversation=%s: %v", row.Id, runID, conversationID, err)
	}
}
