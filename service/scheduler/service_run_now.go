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
	s.reserveRunNow(row.Id, now)
	if err := s.enqueueAndLaunch(ctx, row, now, false); err != nil {
		s.clearRunNowReservation(row.Id, now)
		return err
	}
	return nil
}

func (s *Service) enforceRunNowRateLimit(ctx context.Context, row *schedulepkg.ScheduleView, now time.Time) error {
	if s.hasRecentRunNowReservation(row.Id, now) {
		return ErrRunNowRateLimited
	}
	page, err := s.store.ListRuns(ctx, &schrun.RunListInput{
		ScheduleId: row.Id,
		Has:        &schrun.RunListInputHas{ScheduleId: true},
	}, 1, 100)
	if err != nil {
		return err
	}
	cutoff := now.UTC().Add(-runNowRateLimitWindow)
	if page == nil {
		return nil
	}
	for _, run := range page.Rows {
		if isRecentRunForSchedule(run, cutoff) {
			return ErrRunNowRateLimited
		}
	}
	return nil
}

func (s *Service) reserveRunNow(scheduleID string, at time.Time) {
	if s == nil {
		return
	}
	s.runNowMu.Lock()
	defer s.runNowMu.Unlock()
	if s.runNowReserved == nil {
		s.runNowReserved = map[string]time.Time{}
	}
	s.runNowReserved[scheduleID] = at.UTC()
}

func (s *Service) clearRunNowReservation(scheduleID string, at time.Time) {
	if s == nil {
		return
	}
	s.runNowMu.Lock()
	defer s.runNowMu.Unlock()
	if s.runNowReserved == nil {
		return
	}
	if reserved, ok := s.runNowReserved[scheduleID]; ok && reserved.Equal(at.UTC()) {
		delete(s.runNowReserved, scheduleID)
	}
}

func (s *Service) hasRecentRunNowReservation(scheduleID string, now time.Time) bool {
	if s == nil {
		return false
	}
	s.runNowMu.Lock()
	defer s.runNowMu.Unlock()
	if s.runNowReserved == nil {
		return false
	}
	cutoff := now.UTC().Add(-runNowRateLimitWindow)
	for id, reserved := range s.runNowReserved {
		if reserved.IsZero() || reserved.UTC().Before(cutoff) {
			delete(s.runNowReserved, id)
		}
	}
	reserved, ok := s.runNowReserved[scheduleID]
	return ok && !reserved.UTC().Before(cutoff)
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
