package cohort

import (
	"context"
	"log"
	"os"
	"sync"
	"time"

	"github.com/viant/agently-core/protocol/primitive/observe"
)

// StartReconciliation uses conditional polling only when no
// notification stream owns change detection. A healthy stream performs the
// initial preload and then syncs from change notifications or reconnect hints.
func StartReconciliation(parent context.Context, binding Binding) func() {
	if binding.Source == nil || binding.PollInterval <= 0 || binding.Start != nil {
		return func() {}
	}
	return startArtifactCohortPoll(parent, binding.Source, binding.PollInterval)
}

// startArtifactCohortPoll reconciles a producer whose MCP transport cannot
// deliver resource notifications. The known manifest hash makes unchanged
// ticks compact; request handling never waits for a poll or calls MCP.
func startArtifactCohortPoll(parent context.Context, source Source, interval time.Duration) func() {
	ctx, cancel := context.WithCancel(parent)
	var done sync.WaitGroup
	done.Add(1)
	go func() {
		defer done.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			syncCtx := ctx
			if os.Getenv("BACKEND_PRIMITIVE_TRACE") == "1" {
				started := time.Now()
				syncCtx = observe.WithObserver(ctx, observe.ObserverFunc(func(e observe.Event) {
					end := time.Since(started).Seconds() * 1000
					duration := e.Duration.Seconds() * 1000
					log.Printf("primitive cohort poll phase=%s start_ms=%.3f end_ms=%.3f duration_ms=%.3f calls=1 bytes=%d outcome=%s cache=%s", e.Phase, end-duration, end, duration, e.Bytes, e.Outcome, e.Cache)
				}))
			}
			if err := source.Sync(syncCtx); err != nil {
				if disconnected, ok := source.(interface{ MarkDisconnected() }); ok {
					disconnected.MarkDisconnected()
				}
				if ctx.Err() == nil {
					log.Printf("artifact cohort background reconciliation unavailable")
				}
			}
		}
	}()
	return func() { cancel(); done.Wait() }
}
