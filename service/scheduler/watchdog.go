package scheduler

import (
	"context"
	"log"
	"time"
)

// StartWatchdog begins a background loop that polls for due schedules at the
// configured interval. It blocks until ctx is cancelled.
func (s *Service) StartWatchdog(ctx context.Context) {
	if s.store == nil {
		log.Printf("scheduler: no store configured, watchdog not started")
		return
	}
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.processDue(ctx)
		case <-ctx.Done():
			return
		}
	}
}
