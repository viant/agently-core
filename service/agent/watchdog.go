package agent

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/app/store/data"
	iauth "github.com/viant/agently-core/internal/auth"
	token "github.com/viant/agently-core/internal/auth/token"
	agconv "github.com/viant/agently-core/pkg/agently/conversation"
	agmessagewrite "github.com/viant/agently-core/pkg/agently/message/write"
	agmodelcallwrite "github.com/viant/agently-core/pkg/agently/modelcall/write"
	agrunactive "github.com/viant/agently-core/pkg/agently/run/active"
	agrunstale "github.com/viant/agently-core/pkg/agently/run/stale"
	agrunsteps "github.com/viant/agently-core/pkg/agently/run/steps"
	agrunwrite "github.com/viant/agently-core/pkg/agently/run/write"
	agtoolcallwrite "github.com/viant/agently-core/pkg/agently/toolcall/write"
	agturnactive "github.com/viant/agently-core/pkg/agently/turn/active"
	agturnbyid "github.com/viant/agently-core/pkg/agently/turn/byId"
	agturnlistall "github.com/viant/agently-core/pkg/agently/turn/list"
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
	handleSem     chan struct{}
	handleFn      func(context.Context, *agrunstale.StaleRunsView) error
	repairOnce    sync.Once
}

const defaultRecoveryConcurrency = 4

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
		recoverySem:   make(chan struct{}, defaultRecoveryConcurrency),
		handleSem:     make(chan struct{}, defaultRecoveryConcurrency),
	}
	for _, o := range opts {
		o(w)
	}
	return w
}

// Start begins the watchdog polling loop. It blocks until ctx is canceled.
func (w *Watchdog) Start(ctx context.Context) {
	w.runTerminalArtifactCleanupOnce(ctx)
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

func (w *Watchdog) ensureRecoverySem() chan struct{} {
	if w.recoverySem == nil {
		w.recoverySem = make(chan struct{}, defaultRecoveryConcurrency)
	}
	return w.recoverySem
}

func (w *Watchdog) acquireRecoverySlot(ctx context.Context) error {
	sem := w.ensureRecoverySem()
	select {
	case sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (w *Watchdog) releaseRecoverySlot() {
	sem := w.ensureRecoverySem()
	select {
	case <-sem:
	default:
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

func (w *Watchdog) runTerminalArtifactCleanupOnce(ctx context.Context) {
	if w == nil {
		return
	}
	w.repairOnce.Do(func() {
		repairCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 45*time.Second)
		defer cancel()
		if err := w.repairTerminalTurnArtifacts(repairCtx); err != nil {
			log.Printf("[watchdog] repair terminal turn artifacts: %v", err)
		}
	})
}

func (w *Watchdog) sweepRuns(ctx context.Context, runs []*agrunstale.StaleRunsView) {
	if len(runs) == 0 {
		return
	}
	sem := w.handleSem
	if sem == nil {
		sem = make(chan struct{}, defaultRecoveryConcurrency)
		w.handleSem = sem
	}
	var wg sync.WaitGroup
	for _, run := range runs {
		if run == nil {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(run *agrunstale.StaleRunsView) {
			defer wg.Done()
			defer func() { <-sem }()
			if err := w.handleRun(ctx, run); err != nil {
				log.Printf("[watchdog] handle stale run %s: %v", run.Id, err)
			}
		}(run)
	}
	wg.Wait()
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
			if err := w.failSupersededRunArtifacts(ctx, conversationID, strings.TrimSpace(valueOrEmpty(run.TurnId)), run.Id, fmt.Sprintf("stale turn superseded by active run %s", strings.TrimSpace(activeRun.Id))); err != nil {
				log.Printf("[watchdog] cleanup superseded run artifacts %s: %v", run.Id, err)
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

		if err := w.acquireRecoverySlot(ctx); err != nil {
			return fmt.Errorf("acquire recovery slot: %w", err)
		}
		recoverySlotHeld := true
		releaseRecoverySlot := func() {
			if !recoverySlotHeld {
				return
			}
			recoverySlotHeld = false
			w.releaseRecoverySlot()
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
			releaseRecoverySlot()
			return fmt.Errorf("create resume run: %w", err)
		}

		// Mark old run as failed.
		oldRun := &agrunwrite.MutableRunView{}
		oldRun.SetId(run.Id)
		oldRun.SetStatus("failed")
		oldRun.SetErrorMessage(fmt.Sprintf("worker died, resumed as %s", newRunID))
		oldRun.SetCompletedAt(now)
		if _, err := w.data.PatchRuns(ctx, []*agrunwrite.MutableRunView{oldRun}); err != nil {
			releaseRecoverySlot()
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
					releaseRecoverySlot()
					return fmt.Errorf("terminalize stale active turn: %w", err)
				}
				if err := w.failSupersededRunArtifacts(ctx, conversationID, strings.TrimSpace(active.Id), run.Id, fmt.Sprintf("stale turn superseded by resumed run %s", newRunID)); err != nil {
					log.Printf("[watchdog] cleanup superseded run artifacts %s: %v", run.Id, err)
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
		resumeAsyncCtx := detachResumeContext(resumeCtx)
		go func(resumeCtx context.Context, oldRunID, newRunID string, input *QueryInput) {
			defer releaseRecoverySlot()
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

func (w *Watchdog) repairTerminalTurnArtifacts(ctx context.Context) error {
	if w == nil || w.data == nil {
		return nil
	}
	cursor := ""
	processed := 0
	const pageLimit = 500
	const maxTurns = 5000
	createdSince := time.Now().Add(-60 * 24 * time.Hour)
	for {
		page, err := w.data.GetTurnsPage(ctx, &agturnlistall.TurnRowsInput{
			Statuses:     []string{"failed", "succeeded", "canceled"},
			CreatedSince: createdSince,
			Has: &agturnlistall.TurnRowsInputHas{
				Statuses:     true,
				CreatedSince: true,
			},
		}, &data.PageInput{Limit: pageLimit, Cursor: cursor, Direction: data.DirectionBefore})
		if err != nil {
			return fmt.Errorf("list terminal turns: %w", err)
		}
		if page == nil || len(page.Rows) == 0 {
			return nil
		}
		for _, turn := range page.Rows {
			if turn == nil {
				continue
			}
			runID := strings.TrimSpace(turn.Id)
			if turn.RunId != nil && strings.TrimSpace(*turn.RunId) != "" {
				runID = strings.TrimSpace(*turn.RunId)
			}
			if runID == "" {
				continue
			}
			reason := strings.TrimSpace(valueOrEmpty(turn.ErrorMessage))
			if reason == "" {
				reason = fmt.Sprintf("turn reached terminal status %s", strings.TrimSpace(turn.Status))
			}
			if err := w.failSupersededRunArtifacts(ctx, strings.TrimSpace(turn.ConversationId), strings.TrimSpace(turn.Id), runID, reason); err != nil {
				return fmt.Errorf("repair terminal turn %s: %w", strings.TrimSpace(turn.Id), err)
			}
			processed++
			if processed >= maxTurns {
				return nil
			}
		}
		if !page.HasMore || strings.TrimSpace(page.NextCursor) == "" {
			return nil
		}
		cursor = strings.TrimSpace(page.NextCursor)
	}
}

func (w *Watchdog) failSupersededRunArtifacts(ctx context.Context, conversationID, turnID, runID, reason string) error {
	if w == nil || w.data == nil {
		return nil
	}
	now := time.Now()
	patchedToolCallIDs, err := w.failSupersededRunSteps(ctx, runID, reason, now)
	if err != nil {
		return err
	}
	if strings.TrimSpace(turnID) == "" || strings.TrimSpace(conversationID) == "" {
		return nil
	}
	return w.failSupersededToolMessages(ctx, conversationID, turnID, patchedToolCallIDs)
}

func (w *Watchdog) failSupersededRunSteps(ctx context.Context, runID, reason string, now time.Time) (map[string]struct{}, error) {
	patchedToolCallIDs := map[string]struct{}{}
	if strings.TrimSpace(runID) == "" {
		return patchedToolCallIDs, nil
	}
	page, err := w.data.GetRunStepsPage(ctx, &agrunsteps.RunStepsInput{
		RunID: runID,
		Has:   &agrunsteps.RunStepsInputHas{RunID: true},
	}, &data.PageInput{Limit: 1000})
	if err != nil {
		return nil, fmt.Errorf("load run steps: %w", err)
	}
	if page == nil || len(page.Rows) == 0 {
		return patchedToolCallIDs, nil
	}
	modelRows := make([]*agmodelcallwrite.MutableModelCallView, 0)
	toolRows := make([]*agtoolcallwrite.MutableToolCallView, 0)
	for _, step := range page.Rows {
		if step == nil || isTerminalArtifactStatus(step.Status) || strings.TrimSpace(step.MessageId) == "" {
			continue
		}
		switch strings.TrimSpace(step.StepType) {
		case "model_call":
			row := &agmodelcallwrite.MutableModelCallView{}
			row.SetMessageID(step.MessageId)
			row.SetStatus("failed")
			row.SetErrorMessage(reason)
			row.SetCompletedAt(now)
			modelRows = append(modelRows, row)
		case "tool_call":
			row := &agtoolcallwrite.MutableToolCallView{}
			row.SetMessageID(step.MessageId)
			row.SetStatus("failed")
			row.SetErrorMessage(reason)
			row.SetCompletedAt(now)
			toolRows = append(toolRows, row)
			patchedToolCallIDs[step.MessageId] = struct{}{}
		}
	}
	if len(modelRows) > 0 {
		if _, err := w.data.PatchModelCalls(ctx, modelRows); err != nil {
			return nil, fmt.Errorf("patch superseded model calls: %w", err)
		}
	}
	if len(toolRows) > 0 {
		if _, err := w.data.PatchToolCalls(ctx, toolRows); err != nil {
			return nil, fmt.Errorf("patch superseded tool calls: %w", err)
		}
	}
	return patchedToolCallIDs, nil
}

func (w *Watchdog) failSupersededToolMessages(ctx context.Context, conversationID, turnID string, skipToolCallIDs map[string]struct{}) error {
	conv, err := w.data.GetConversation(ctx, conversationID, &agconv.ConversationInput{
		IncludeTranscript: true,
		IncludeToolCall:   true,
		Has: &agconv.ConversationInputHas{
			IncludeTranscript: true,
			IncludeToolCall:   true,
		},
	})
	if err != nil {
		return fmt.Errorf("load conversation tool messages: %w", err)
	}
	if conv == nil || len(conv.Transcript) == 0 {
		return nil
	}
	runningToolCalls := map[string]struct{}{}
	messageRows := map[string]*agconv.MessageView{}
	for _, turn := range conv.Transcript {
		if turn == nil || strings.TrimSpace(turn.Id) != turnID {
			continue
		}
		for _, msg := range turn.Message {
			if msg == nil || strings.TrimSpace(msg.Id) == "" {
				continue
			}
			if isToolOpMessage(msg) {
				messageRows[msg.Id] = msg
				if tc := msg.MessageToolCall; tc != nil && !isTerminalArtifactStatus(tc.Status) {
					runningToolCalls[msg.Id] = struct{}{}
				}
			}
			for _, tm := range msg.ToolMessage {
				if tm == nil || tm.ToolCall == nil || strings.TrimSpace(tm.Id) == "" {
					continue
				}
				if !isTerminalArtifactStatus(tm.ToolCall.Status) {
					runningToolCalls[tm.Id] = struct{}{}
				}
			}
		}
	}
	if len(messageRows) == 0 && len(runningToolCalls) == 0 {
		return nil
	}
	now := time.Now()
	rows := make([]*agmessagewrite.MutableMessageView, 0)
	toolRows := make([]*agtoolcallwrite.MutableToolCallView, 0)
	for _, msg := range messageRows {
		_, toolCallRunning := runningToolCalls[msg.Id]
		messageTerminal := isTerminalArtifactStatus(valueOrEmpty(msg.Status))
		if messageTerminal && !toolCallRunning {
			continue
		}
		if !messageTerminal {
			row := &agmessagewrite.MutableMessageView{}
			row.SetId(msg.Id)
			row.SetConversationID(conversationID)
			row.SetStatus("failed")
			rows = append(rows, row)
		}

		if _, ok := skipToolCallIDs[msg.Id]; ok {
			continue
		}
		if !toolCallRunning {
			continue
		}
		toolRow := &agtoolcallwrite.MutableToolCallView{}
		toolRow.SetMessageID(msg.Id)
		toolRow.SetStatus("failed")
		toolRow.SetErrorMessage("tool message terminalized after turn ended")
		toolRow.SetCompletedAt(now)
		toolRows = append(toolRows, toolRow)
	}
	if len(rows) == 0 && len(toolRows) == 0 {
		return nil
	}
	if len(rows) > 0 {
		if _, err := w.data.PatchMessages(ctx, rows); err != nil {
			return fmt.Errorf("patch superseded tool messages: %w", err)
		}
	}
	if len(toolRows) > 0 {
		if _, err := w.data.PatchToolCalls(ctx, toolRows); err != nil {
			return fmt.Errorf("patch tool calls by message linkage: %w", err)
		}
	}
	return nil
}

func isToolOpMessage(msg *agconv.MessageView) bool {
	if msg == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(msg.Type), "tool_op") || strings.EqualFold(strings.TrimSpace(msg.Role), "tool")
}

func isTerminalArtifactStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed", "failed", "canceled", "cancelled", "succeeded":
		return true
	default:
		return false
	}
}
