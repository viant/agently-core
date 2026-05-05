package agent

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/app/store/data"
	token "github.com/viant/agently-core/internal/auth/token"
	agrunstale "github.com/viant/agently-core/pkg/agently/run/stale"
	agrunwrite "github.com/viant/agently-core/pkg/agently/run/write"
	agturnactive "github.com/viant/agently-core/pkg/agently/turn/active"
	runtimerecovery "github.com/viant/agently-core/runtime/recovery"
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
	w.sweep(ctx)
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
	if shouldSkipStaleRun(run) {
		return nil
	}
	// Try to resume if we have auth context and a conversation to continue.
	if run.ConversationId != nil && strings.TrimSpace(*run.ConversationId) != "" {
		conversationID := strings.TrimSpace(*run.ConversationId)
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
		if w.agent != nil {
			w.agent.populateInteractiveRunRuntime(newRun, now)
		} else if strings.TrimSpace(w.workerHost) != "" {
			newRun.SetWorkerHost(strings.TrimSpace(w.workerHost))
			newRun.SetLastHeartbeatAt(now)
		}
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
		// Also terminalize the stale active turn before resuming. If the old turn
		// remains `running`, agent.Query will treat it as an active turn and queue
		// the recovery behind it instead of taking over the stale work.
		if w.agent != nil && w.agent.conversation != nil && w.data != nil {
			active, err := w.data.GetActiveTurn(ctx, &agturnactive.ActiveTurnsInput{
				ConversationID: conversationID,
				Has:            &agturnactive.ActiveTurnsInputHas{ConversationID: true},
			})
			if err != nil {
				return fmt.Errorf("load active turn for stale run: %w", err)
			}
			if active != nil && strings.TrimSpace(active.Id) != "" {
				upd := apiconv.NewTurn()
				upd.SetId(strings.TrimSpace(active.Id))
				upd.SetStatus("failed")
				upd.SetErrorMessage(fmt.Sprintf("stale turn superseded by resumed run %s", newRunID))
				if err := w.agent.conversation.PatchTurn(ctx, upd); err != nil {
					return fmt.Errorf("terminalize stale active turn: %w", err)
				}
			}
		}

		// Re-invoke query with restored context synchronously. Recovery is already
		// running inside a watchdog sweep; spawning one goroutine per stale run
		// creates a DB-thrashing recovery stampede on SQLite and leaves fresh
		// resumed runs stuck in `running` when queueing fails under lock
		// contention.
		agentID := ""
		if run.AgentId != nil {
			agentID = *run.AgentId
		}
		resumeCtx = runtimerecovery.WithMode(resumeCtx, runtimerecovery.ModeResume)
		input := &QueryInput{
			AgentID:        agentID,
			ConversationID: conversationID,
			MessageID:      newRunID,
			Query:          "", // continue existing conversation
		}
		out := &QueryOutput{}
		if err := w.agent.Query(resumeCtx, input, out); err != nil {
			log.Printf("[watchdog] resume run %s (was %s): %v", newRunID, run.Id, err)
			failResume := &agrunwrite.MutableRunView{}
			failResume.SetId(newRunID)
			failResume.SetStatus("failed")
			failResume.SetErrorMessage(fmt.Sprintf("resume failed: %v", err))
			failResume.SetCompletedAt(time.Now())
			if _, patchErr := w.data.PatchRuns(context.Background(), []*agrunwrite.MutableRunView{failResume}); patchErr != nil {
				log.Printf("[watchdog] mark resumed run failed %s: %v", newRunID, patchErr)
			}
		}
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

func shouldSkipStaleRun(run *agrunstale.StaleRunsView) bool {
	if run == nil {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(run.ConversationKind), "scheduled") {
		return true
	}
	return false
}
