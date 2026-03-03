package agent

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/viant/agently-core/app/store/data"
	token "github.com/viant/agently-core/internal/auth/token"
	agrunstale "github.com/viant/agently-core/pkg/agently/run/stale"
	agrunwrite "github.com/viant/agently-core/pkg/agently/run/write"
)

// Watchdog periodically detects stale runs and either marks them failed
// or resumes them (when the conversation still has pending work).
type Watchdog struct {
	data          data.Service
	agent         *Service
	tokenProvider token.Provider
	interval      time.Duration
	workerHost    string
}

// WatchdogOption configures a Watchdog.
type WatchdogOption func(*Watchdog)

// WithWatchdogInterval sets the polling interval.
func WithWatchdogInterval(d time.Duration) WatchdogOption {
	return func(w *Watchdog) { w.interval = d }
}

// WithWatchdogTokenProvider sets the token provider for restoring auth on resume.
func WithWatchdogTokenProvider(p token.Provider) WatchdogOption {
	return func(w *Watchdog) { w.tokenProvider = p }
}

// NewWatchdog creates a watchdog for stale run detection and resume.
func NewWatchdog(data data.Service, agent *Service, opts ...WatchdogOption) *Watchdog {
	hostname, _ := os.Hostname()
	w := &Watchdog{
		data:       data,
		agent:      agent,
		interval:   60 * time.Second,
		workerHost: hostname,
	}
	for _, o := range opts {
		o(w)
	}
	return w
}

// Start begins the watchdog polling loop. It blocks until ctx is canceled.
func (w *Watchdog) Start(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.sweep(ctx)
		}
	}
}

func (w *Watchdog) sweep(ctx context.Context) {
	threshold := time.Now().Add(-2 * w.interval)
	input := &agrunstale.StaleRunsInput{
		HeartbeatBefore: threshold,
		WorkerHost:      w.workerHost,
		Has:             &agrunstale.StaleRunsInputHas{HeartbeatBefore: true, WorkerHost: true},
	}
	runs, err := w.data.ListStaleRuns(ctx, input)
	if err != nil {
		log.Printf("[watchdog] list stale runs: %v", err)
		return
	}
	for _, run := range runs {
		if err := w.handleStaleRun(ctx, run); err != nil {
			log.Printf("[watchdog] handle stale run %s: %v", run.Id, err)
		}
	}
}

func (w *Watchdog) handleStaleRun(ctx context.Context, run *agrunstale.StaleRunsView) error {
	// Try to resume if we have auth context and a conversation to continue.
	if run.ConversationId != nil && strings.TrimSpace(*run.ConversationId) != "" {
		resumeCtx := ctx

		// Restore auth from SecurityContext if available.
		if run.SecurityContext != nil && *run.SecurityContext != "" {
			var err error
			var sd *token.SecurityData
			resumeCtx, sd, err = token.RestoreSecurityContext(resumeCtx, *run.SecurityContext)
			if err != nil {
				log.Printf("[watchdog] restore security context for run %s: %v", run.Id, err)
			}
			// Also populate token provider cache so EnsureTokens works.
			if sd != nil && w.tokenProvider != nil && sd.Subject != "" {
				key := token.Key{Subject: sd.Subject, Provider: sd.Provider}
				_, _ = w.tokenProvider.EnsureTokens(resumeCtx, key)
			}
		}

		// Create new run as a resume of the stale one.
		newRunID := uuid.New().String()
		newRun := &agrunwrite.MutableRunView{}
		newRun.SetId(newRunID)
		newRun.SetStatus("running")
		newRun.SetResumedFromRunID(run.Id)
		if run.ConversationId != nil {
			newRun.SetConversationID(*run.ConversationId)
		}
		if run.AgentId != nil {
			newRun.SetAgentID(*run.AgentId)
		}
		if run.EffectiveUserId != nil {
			newRun.SetEffectiveUserID(*run.EffectiveUserId)
		}
		now := time.Now()
		newRun.SetCreatedAt(now)
		newRun.SetStartedAt(now)
		if _, err := w.data.PatchRuns(ctx, []*agrunwrite.MutableRunView{newRun}); err != nil {
			return fmt.Errorf("create resume run: %w", err)
		}

		// Mark old run as failed.
		oldRun := &agrunwrite.MutableRunView{}
		oldRun.SetId(run.Id)
		oldRun.SetStatus("failed")
		oldRun.SetErrorMessage(fmt.Sprintf("worker died, resumed as %s", newRunID))
		oldRun.SetCompletedAt(now)
		if _, err := w.data.PatchRuns(ctx, []*agrunwrite.MutableRunView{oldRun}); err != nil {
			return fmt.Errorf("mark stale run failed: %w", err)
		}

		// Re-invoke query with restored context.
		go func() {
			agentID := ""
			if run.AgentId != nil {
				agentID = *run.AgentId
			}
			input := &QueryInput{
				AgentID:        agentID,
				ConversationID: *run.ConversationId,
				MessageID:      newRunID,
				Query:          "", // continue existing conversation
			}
			out := &QueryOutput{}
			if err := w.agent.Query(resumeCtx, input, out); err != nil {
				log.Printf("[watchdog] resume run %s (was %s): %v", newRunID, run.Id, err)
			}
		}()
		return nil
	}

	// No conversation to resume — just mark as failed.
	failRun := &agrunwrite.MutableRunView{}
	failRun.SetId(run.Id)
	failRun.SetStatus("failed")
	failRun.SetErrorMessage("worker died, no conversation to resume")
	failRun.SetCompletedAt(time.Now())
	_, err := w.data.PatchRuns(ctx, []*agrunwrite.MutableRunView{failRun})
	return err
}
