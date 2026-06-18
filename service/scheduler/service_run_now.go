package scheduler

import (
	"context"
	"errors"
	"fmt"
	"time"

	schrun "github.com/viant/agently-core/pkg/agently/scheduler/run"
	schedulepkg "github.com/viant/agently-core/pkg/agently/scheduler/schedule"
)

const runNowRateLimitWindow = time.Minute

var ErrRunNowRateLimited = errors.New("You can't run this schedule more than once per minute. Please wait before using Run Now again.")

func (s *Service) runNowOnDemand(ctx context.Context, row *schedulepkg.ScheduleView) error {
	if s == nil || s.store == nil || row == nil {
		return fmt.Errorf("scheduler service not initialized")
	}
	if s.agent == nil {
		return fmt.Errorf("scheduler service not initialized")
	}
	now := time.Now().UTC()
	if err := s.enforceRunNowRateLimit(ctx, row, now); err != nil {
		return err
	}
	return s.enqueueAndLaunch(ctx, row, now, false)
}

func (s *Service) enforceRunNowRateLimit(ctx context.Context, row *schedulepkg.ScheduleView, now time.Time) error {
	runs, err := s.store.ListRunsForDue(ctx, row.Id, nil, nil)
	if err != nil {
		return err
	}
	cutoff := now.UTC().Add(-runNowRateLimitWindow)
	for _, run := range runs {
		if isRecentRunForSchedule(run, cutoff) {
			return ErrRunNowRateLimited
		}
	}
	return nil
}

func isRecentRunForSchedule(run *schrun.RunView, cutoff time.Time) bool {
	if run == nil {
		return false
	}
	createdAt := run.CreatedAt
	if createdAt.IsZero() && run.StartedAt != nil {
		createdAt = *run.StartedAt
	}
	if createdAt.IsZero() {
		return false
	}
	return !createdAt.UTC().Before(cutoff.UTC())
}
