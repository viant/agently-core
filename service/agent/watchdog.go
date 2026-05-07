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
	iauth "github.com/viant/agently-core/internal/auth"
	token "github.com/viant/agently-core/internal/auth/token"
	agrunactive "github.com/viant/agently-core/pkg/agently/run/active"
	agrunstale "github.com/viant/agently-core/pkg/agently/run/stale"
	agrunwrite "github.com/viant/agently-core/pkg/agently/run/write"
	agturnactive "github.com/viant/agently-core/pkg/agently/turn/active"
	agturnbyid "github.com/viant/agently-core/pkg/agently/turn/byId"
	agruntime "github.com/viant/agently-core/runtime"
	runtimerecovery "github.com/viant/agently-core/runtime/recovery"
)

// Watchdog periodically detects stale runs and either marks them failed
// or resumes them (when the conversation still has pending work).
type Watchdog struct {
	data          data.Service
	agent         *Service
	tokenProvider token.Provider
	interval      time.Duration
	handleTimeout time.Duration
	workerHost    string
	recoverySem   chan struct{}
	handleFn      func(context.Context, *agrunstale.StaleRunsView) error
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

// WithWatchdogHandleTimeout bounds recovery work per stale run so one blocked
// recovery cannot stall the entire watchdog sweep.
func WithWatchdogHandleTimeout(d time.Duration) WatchdogOption {
	return func(w *Watchdog) { w.handleTimeout = d }
}

// NewWatchdog creates a watchdog for stale run detection and resume.
func NewWatchdog(data data.Service, agent *Service, opts ...WatchdogOption) *Watchdog {
	hostname, _ := os.Hostname()
	w := &Watchdog{
		data:          data,
		agent:         agent,
		interval:      60 * time.Second,
		handleTimeout: 15 * time.Second,
		workerHost:    hostname,
		recoverySem:   make(chan struct{}, 2),
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
	w.sweepRuns(ctx, runs)
}

func (w *Watchdog) sweepRuns(ctx context.Context, runs []*agrunstale.StaleRunsView) {
	for _, run := range runs {
		if err := w.handleRun(ctx, run); err != nil {
			log.Printf("[watchdog] handle stale run %s: %v", run.Id, err)
		}
	}
}

func (w *Watchdog) handleRun(ctx context.Context, run *agrunstale.StaleRunsView) error {
	runCtx := context.WithoutCancel(ctx)
	cancel := func() {}
	if w.handleTimeout > 0 {
		runCtx, cancel = context.WithTimeout(runCtx, w.handleTimeout)
	}
	defer cancel()
	if w.handleFn != nil {
		return w.handleFn(runCtx, run)
	}
	return w.handleStaleRun(runCtx, run)
}

func (w *Watchdog) handleStaleRun(ctx context.Context, run *agrunstale.StaleRunsView) error {
	if shouldSkipStaleRun(run) {
		return nil
	}
	// Legacy backlog shape: a prior recovery created a queued turn whose id
	// matches the run id, but never linked run.turn_id. Repair that linkage and
	// explicitly drain the queued turn instead of spawning another resumed run.
	if run.ConversationId != nil && strings.TrimSpace(*run.ConversationId) != "" && strings.TrimSpace(valueOrEmpty(run.TurnId)) == "" {
		queuedTurn, err := w.data.GetTurnByID(ctx, &agturnbyid.TurnLookupInput{
			ID:  run.Id,
			Has: &agturnbyid.TurnLookupInputHas{ID: true},
		})
		if err != nil {
			return fmt.Errorf("lookup queued recovery turn: %w", err)
		}
		if queuedTurn != nil && strings.EqualFold(strings.TrimSpace(queuedTurn.Status), "queued") {
			upd := &agrunwrite.MutableRunView{}
			upd.SetId(run.Id)
			upd.SetTurnID(run.Id)
			now := time.Now()
			if w.agent != nil {
				w.agent.populateInteractiveRunRuntime(upd, now)
			} else if strings.TrimSpace(w.workerHost) != "" {
				upd.SetWorkerHost(strings.TrimSpace(w.workerHost))
				upd.SetLastHeartbeatAt(now)
			}
			if _, patchErr := w.data.PatchRuns(ctx, []*agrunwrite.MutableRunView{upd}); patchErr != nil {
				return fmt.Errorf("repair queued recovery run linkage: %w", patchErr)
			}
			if w.agent != nil {
				w.agent.triggerQueueDrain(strings.TrimSpace(*run.ConversationId))
			}
			return nil
		}
	}
	// Try to resume if we have auth context and a conversation to continue.
	if run.ConversationId != nil && strings.TrimSpace(*run.ConversationId) != "" {
		conversationID := strings.TrimSpace(*run.ConversationId)
		if activeRun, err := w.data.GetActiveRun(ctx, &agrunactive.ActiveRunsInput{
			ConversationId: conversationID,
			Has:            &agrunactive.ActiveRunsInputHas{ConversationId: true},
		}); err != nil {
			return fmt.Errorf("load active run for stale conversation: %w", err)
		} else if activeRunSupersedesStale(run.Id, activeRun) {
			now := time.Now()
			oldRun := &agrunwrite.MutableRunView{}
			oldRun.SetId(run.Id)
			oldRun.SetStatus("failed")
			oldRun.SetErrorMessage(fmt.Sprintf("worker died, superseded by active run %s", strings.TrimSpace(activeRun.Id)))
			oldRun.SetCompletedAt(now)
			if _, err := w.data.PatchRuns(ctx, []*agrunwrite.MutableRunView{oldRun}); err != nil {
				return fmt.Errorf("mark stale run superseded: %w", err)
			}
			return nil
		}
		resumeCtx := ctx
		var sd *token.SecurityData

		// Restore auth from SecurityContext if available.
		if run.SecurityContext != nil && *run.SecurityContext != "" {
			var err error
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
		resumeUserID := resolveResumeUserID(run, sd)
		if resumeUserID != "" && strings.TrimSpace(iauth.EffectiveUserID(resumeCtx)) == "" {
			resumeCtx = iauth.WithUserInfo(resumeCtx, &iauth.UserInfo{Subject: resumeUserID})
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
		var resumeSkillContext *agruntime.Context
		if w.agent != nil && w.agent.conversation != nil && w.data != nil {
			active, err := w.data.GetActiveTurn(ctx, &agturnactive.ActiveTurnsInput{
				ConversationID: conversationID,
				Has:            &agturnactive.ActiveTurnsInputHas{ConversationID: true},
			})
			if err != nil {
				return fmt.Errorf("load active turn for stale run: %w", err)
			}
			if active != nil && strings.TrimSpace(active.Id) != "" {
				resumeSkillContext = loadInlineSkillContextForTurn(ctx, w.agent.conversation, conversationID, strings.TrimSpace(active.Id))
				upd := apiconv.NewTurn()
				upd.SetId(strings.TrimSpace(active.Id))
				upd.SetStatus("failed")
				upd.SetErrorMessage(fmt.Sprintf("stale turn superseded by resumed run %s", newRunID))
				if err := w.agent.conversation.PatchTurn(ctx, upd); err != nil {
					return fmt.Errorf("terminalize stale active turn: %w", err)
				}
				// The normal finalizeTurn path triggers queue drain after a turn
				// becomes terminal. This watchdog path bypasses finalizeTurn, so do
				// the same queue-drain handoff explicitly for old queued recovery
				// attempts in the conversation.
				w.agent.triggerQueueDrain(conversationID)
			}
		}

		agentID := ""
		if run.AgentId != nil {
			agentID = *run.AgentId
		}
		resumeCtx = runtimerecovery.WithMode(resumeCtx, runtimerecovery.ModeResume)
		input := &QueryInput{
			AgentID:        agentID,
			ConversationID: conversationID,
			MessageID:      newRunID,
			UserId:         resumeUserID,
			Query:          "", // continue existing conversation
		}
		if resumeSkillContext != nil {
			input.Runtime = resumeSkillContext
		}
		// Resume query asynchronously with bounded concurrency so the watchdog can
		// continue sweeping newer stale runs without reopening the old
		// unbounded-goroutine DB storm.
		sem := w.recoverySem
		if sem == nil {
			sem = make(chan struct{}, 2)
			w.recoverySem = sem
		}
		sem <- struct{}{}
		resumeAsyncCtx := detachResumeContext(resumeCtx)
		go func(resumeCtx context.Context, oldRunID, newRunID string, input *QueryInput) {
			defer func() { <-sem }()
			out := &QueryOutput{}
			if err := w.agent.Query(resumeCtx, input, out); err != nil {
				log.Printf("[watchdog] resume run %s (was %s): %v", newRunID, oldRunID, err)
				failResume := &agrunwrite.MutableRunView{}
				failResume.SetId(newRunID)
				failResume.SetStatus("failed")
				failResume.SetErrorMessage(fmt.Sprintf("resume failed: %v", err))
				failResume.SetCompletedAt(time.Now())
				if _, patchErr := w.data.PatchRuns(context.Background(), []*agrunwrite.MutableRunView{failResume}); patchErr != nil {
					log.Printf("[watchdog] mark resumed run failed %s: %v", newRunID, patchErr)
				}
				if w.agent != nil && strings.TrimSpace(input.ConversationID) != "" {
					if convErr := w.agent.patchConversationStatus(context.Background(), strings.TrimSpace(input.ConversationID), "failed"); convErr != nil {
						log.Printf("[watchdog] mark resumed conversation failed %s: %v", input.ConversationID, convErr)
					}
					w.agent.triggerQueueDrain(strings.TrimSpace(input.ConversationID))
				}
			}
		}(resumeAsyncCtx, run.Id, newRunID, input)
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

func detachResumeContext(ctx context.Context) context.Context {
	return context.WithoutCancel(ctx)
}

func resolveResumeUserID(run *agrunstale.StaleRunsView, sd *token.SecurityData) string {
	if sd != nil {
		if subject := strings.TrimSpace(sd.Subject); subject != "" {
			return subject
		}
	}
	if run == nil || run.EffectiveUserId == nil {
		return ""
	}
	return strings.TrimSpace(*run.EffectiveUserId)
}

func shouldSkipStaleRun(run *agrunstale.StaleRunsView) bool {
	if run == nil {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(run.ConversationKind), "scheduled") {
		return true
	}
	if run.ResumedFromRunId != nil && strings.TrimSpace(*run.ResumedFromRunId) != "" {
		return true
	}
	return false
}

func activeRunSupersedesStale(staleRunID string, activeRun *agrunactive.ActiveRunsView) bool {
	if activeRun == nil {
		return false
	}
	activeID := strings.TrimSpace(activeRun.Id)
	if activeID == "" {
		return false
	}
	return activeID != strings.TrimSpace(staleRunID)
}
