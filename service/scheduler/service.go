package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	convcli "github.com/viant/agently-core/app/store/conversation"
	iauth "github.com/viant/agently-core/internal/auth"
	token "github.com/viant/agently-core/internal/auth/token"
	agrunwrite "github.com/viant/agently-core/pkg/agently/run/write"
	schrun "github.com/viant/agently-core/pkg/agently/scheduler/run"
	schedulepkg "github.com/viant/agently-core/pkg/agently/scheduler/schedule"
	schedwrite "github.com/viant/agently-core/pkg/agently/scheduler/schedule/write"
	"github.com/viant/agently-core/runtime/memory"
	agentsvc "github.com/viant/agently-core/service/agent"
	"github.com/viant/scy"
	scyauth "github.com/viant/scy/auth"
	"github.com/viant/scy/auth/authorizer"
	"golang.org/x/oauth2"
)

type oauthAuthorizer interface {
	Authorize(ctx context.Context, command *authorizer.Command) (*oauth2.Token, error)
}

// Service manages persisted scheduler CRUD and execution of due schedules.
type Service struct {
	store           Store
	agent           *agentsvc.Service
	conversation    convcli.Client
	interval        time.Duration
	tokenProvider   token.Provider
	scyService      *scy.Service
	authCfg         *iauth.Config
	userCredAuthCfg *UserCredAuthConfig
	oauthAuthz      oauthAuthorizer
	leaseOwner      string
	leaseTTL        time.Duration
}

const (
	defaultRunStartTimeout = 2 * time.Minute
	defaultStaleRunTimeout = 20 * time.Minute
	staleRunGrace          = 15 * time.Second
	runLeaseCallTimeout    = 5 * time.Second
	minRunHeartbeatEvery   = 3 * time.Second
)

// New creates a scheduler service.
func New(store Store, agent *agentsvc.Service, opts ...Option) *Service {
	s := &Service{
		store:    store,
		agent:    agent,
		interval: 30 * time.Second,
		leaseTTL: 60 * time.Second,
	}
	for _, o := range opts {
		if o != nil {
			o(s)
		}
	}
	return s
}

// Option customizes the scheduler service.
type Option func(*Service)

// WithInterval sets the watchdog polling interval.
func WithInterval(d time.Duration) Option {
	return func(s *Service) { s.interval = d }
}

// WithConversationClient sets the conversation client used to annotate scheduled conversations.
func WithConversationClient(client convcli.Client) Option {
	return func(s *Service) { s.conversation = client }
}

// WithTokenProvider sets the token provider for credential management.
func WithTokenProvider(p token.Provider) Option {
	return func(s *Service) { s.tokenProvider = p }
}

// WithScyService sets the scy service for loading encrypted secrets.
func WithScyService(sv *scy.Service) Option {
	return func(s *Service) { s.scyService = sv }
}

// WithAuthConfig sets auth configuration used by scheduler OOB user_cred flow.
func WithAuthConfig(cfg *iauth.Config) Option {
	return func(s *Service) { s.authCfg = cfg }
}

// Get returns a schedule by ID.
func (s *Service) Get(ctx context.Context, id string) (*Schedule, error) {
	row, err := s.store.Get(ctx, id)
	if err != nil || row == nil {
		return nil, err
	}
	return toPublicSchedule(row), nil
}

// List returns schedules visible to the caller.
func (s *Service) List(ctx context.Context) ([]*Schedule, error) {
	rows, err := s.store.List(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]*Schedule, 0, len(rows))
	for _, row := range rows {
		if row == nil {
			continue
		}
		result = append(result, toPublicSchedule(row))
	}
	return result, nil
}

// Upsert creates or updates a schedule using marker-based partial updates.
func (s *Service) Upsert(ctx context.Context, schedule *Schedule) error {
	if schedule == nil {
		return fmt.Errorf("schedule is required")
	}
	var existing *schedulepkg.ScheduleView
	if id := strings.TrimSpace(schedule.ID); id != "" {
		row, err := s.store.Get(ctx, id)
		if err != nil {
			return err
		}
		existing = row
	}
	mut := toMutableSchedule(schedule, existing != nil)
	return s.store.PatchSchedule(ctx, mut)
}

// Delete is not exposed on the current SDK surface.
func (s *Service) Delete(id string) error {
	_ = id
	return errors.New("scheduler delete is not implemented")
}

// RunNow enqueues and starts an immediate execution for a schedule.
func (s *Service) RunNow(ctx context.Context, id string) error {
	row, err := s.store.Get(ctx, id)
	if err != nil {
		return fmt.Errorf("get schedule: %w", err)
	}
	if row == nil {
		return fmt.Errorf("schedule %s not found", id)
	}
	return s.enqueueAndLaunch(ctx, row, time.Now().UTC(), false)
}

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
			defer func() {
				_, _ = s.store.ReleaseScheduleLease(context.Background(), row.Id, s.leaseOwner)
			}()

			runs, runErr := s.getRunsForDueCheck(ctx, row.Id, scheduledFor, includeScheduledSlot)
			if runErr != nil {
				err = runErr
				return
			}

			runs, runErr = s.cleanupStaleRuns(ctx, row, runs, now)
			if runErr != nil {
				err = runErr
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

// processDue is used by the watchdog ticker.
func (s *Service) processDue(ctx context.Context) {
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

	go s.executeRun(context.WithoutCancel(ctx), row, runID, scheduledFor.UTC())
	return nil
}

func (s *Service) executeRun(ctx context.Context, row *schedulepkg.ScheduleView, runID string, scheduledFor time.Time) {
	if s == nil || row == nil || s.agent == nil {
		return
	}
	s.ensureLeaseConfig()

	runCtx := memory.MergeDiscoveryMode(ctx, memory.DiscoveryMode{
		Scheduler:     true,
		ScheduleID:    strings.TrimSpace(row.Id),
		ScheduleRunID: strings.TrimSpace(runID),
	})
	if cred := strings.TrimSpace(valueOrEmpty(row.UserCredURL)); cred != "" {
		logAuthRunf(row.Id, runID, scheduleUserID(runCtx, row), "user_cred detected ref_kind=%q", userCredRefKind(cred))
		var err error
		runCtx, err = s.applyUserCred(runCtx, cred)
		if err != nil {
			logAuthRunf(row.Id, runID, scheduleUserID(runCtx, row), "user_cred apply failed err=%v", err)
		}
	}

	userID := scheduleUserID(runCtx, row)
	if userID != "" && strings.TrimSpace(iauth.EffectiveUserID(runCtx)) == "" {
		runCtx = iauth.WithUserInfo(runCtx, &iauth.UserInfo{Subject: userID})
	}
	logAuthRunf(row.Id, runID, scheduleUserID(runCtx, row), "using effective user")

	stopHeartbeat := func() {}
	defer s.releaseRunLease(context.Background(), runID)
	defer func() { stopHeartbeat() }()
	if claimed, claimErr := s.tryClaimRunLease(runCtx, runID, time.Now().UTC()); claimErr != nil {
		log.Printf("scheduler: claim run lease schedule=%s run=%s owner=%s: %v", row.Id, runID, strings.TrimSpace(s.leaseOwner), claimErr)
	} else if claimed {
		stopHeartbeat = s.startRunLeaseHeartbeat(runCtx, runID)
	}

	input := &agentsvc.QueryInput{
		MessageID:     runID,
		AgentID:       strings.TrimSpace(row.AgentRef),
		UserId:        userID,
		Query:         schedulePrompt(row),
		ModelOverride: strings.TrimSpace(valueOrEmpty(row.ModelOverride)),
		ScheduleId:    strings.TrimSpace(row.Id),
	}
	output := &agentsvc.QueryOutput{}
	queryCtx, queryCancel := context.WithTimeout(runCtx, scheduleExecutionTimeout(row))
	err := s.agent.Query(queryCtx, input, output)
	queryCancel()

	// Re-assert scheduler metadata after agent execution and persist the
	// conversation ID produced during the run.
	runPatch := &agrunwrite.MutableRunView{}
	runPatch.SetId(runID)
	runPatch.SetScheduleID(row.Id)
	runPatch.SetConversationKind("scheduled")
	runPatch.SetScheduledFor(scheduledFor.UTC())
	runPatch.SetAgentID(strings.TrimSpace(row.AgentRef))
	if userID != "" {
		runPatch.SetEffectiveUserID(userID)
	}
	if cred := strings.TrimSpace(valueOrEmpty(row.UserCredURL)); cred != "" {
		runPatch.SetUserCredURL(cred)
	}
	if output.ConversationID != "" {
		runPatch.SetConversationID(output.ConversationID)
	}
	if patchErr := s.store.PatchRuns(context.Background(), []*agrunwrite.MutableRunView{runPatch}); patchErr != nil {
		log.Printf("scheduler: patch run metadata schedule=%s run=%s: %v", row.Id, runID, patchErr)
	}

	status := "succeeded"
	errMsg := ""
	if err != nil {
		status = "failed"
		errMsg = err.Error()
		failPatch := &agrunwrite.MutableRunView{}
		failPatch.SetId(runID)
		failPatch.SetStatus(status)
		failPatch.SetErrorMessage(errMsg)
		failPatch.SetCompletedAt(time.Now().UTC())
		if patchErr := s.store.PatchRuns(context.Background(), []*agrunwrite.MutableRunView{failPatch}); patchErr != nil {
			log.Printf("scheduler: patch failed run schedule=%s run=%s: %v", row.Id, runID, patchErr)
		}
		log.Printf("scheduler: execute schedule=%s run=%s: %v", row.Id, runID, err)
	}

	if output.ConversationID != "" {
		s.annotateConversation(context.Background(), row, output.ConversationID, runID)
	}
	if patchErr := s.patchScheduleResult(context.Background(), row, status, errMsg, timePtrUTC(time.Now().UTC()), false, time.Now().UTC()); patchErr != nil {
		log.Printf("scheduler: patch schedule result schedule=%s run=%s: %v", row.Id, runID, patchErr)
	}
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

func (s *Service) getRunsForDueCheck(ctx context.Context, scheduleID string, scheduledFor time.Time, includeScheduledSlot bool) ([]*schrun.RunView, error) {
	var runs []*schrun.RunView
	seen := map[string]struct{}{}
	add := func(items []*schrun.RunView) {
		for _, item := range items {
			if item == nil {
				continue
			}
			id := strings.TrimSpace(item.Id)
			if id == "" {
				continue
			}
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			runs = append(runs, item)
		}
	}

	if includeScheduledSlot {
		slotRuns, err := s.store.ListRunsForDue(ctx, scheduleID, &scheduledFor, nil)
		if err != nil {
			return nil, err
		}
		add(slotRuns)
	}

	activeRuns, err := s.store.ListRunsForDue(ctx, scheduleID, nil, []string{"succeeded", "failed", "skipped", "canceled"})
	if err != nil {
		return nil, err
	}
	add(activeRuns)
	return runs, nil
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
	if run == nil || !strings.EqualFold(strings.TrimSpace(run.Status), "pending") {
		return "", false
	}
	if run.CompletedAt != nil && !run.CompletedAt.IsZero() {
		return "", false
	}
	if run.StartedAt != nil && !run.StartedAt.IsZero() {
		return "", false
	}
	if strings.TrimSpace(valueOrEmpty(run.ConversationId)) != "" {
		return "", false
	}
	if run.CreatedAt.IsZero() {
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
	return fmt.Sprintf(
		"stale pending run detected: pending without conversation/start for %s",
		age.Round(time.Second),
	), true
}

func (s *Service) staleActiveRunReason(row *schedulepkg.ScheduleView, run *schrun.RunView, now time.Time) (string, bool) {
	runStart, timeout, stale := s.isStaleRun(row, run, now)
	if !stale {
		return "", false
	}
	return fmt.Sprintf(
		"stale scheduled run detected: status=%s run_start=%s timeout=%s",
		strings.TrimSpace(run.Status),
		runStart.UTC().Format(time.RFC3339Nano),
		timeout,
	), true
}

func (s *Service) isStaleRun(row *schedulepkg.ScheduleView, run *schrun.RunView, now time.Time) (time.Time, time.Duration, bool) {
	if run == nil {
		return time.Time{}, 0, false
	}
	if run.CompletedAt != nil && !run.CompletedAt.IsZero() {
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
	runID := strings.TrimSpace(run.Id)
	if runID == "" {
		return nil
	}
	errs := make([]error, 0, 3)
	claimed, claimErr := s.tryClaimRunLease(ctx, runID, now)
	if claimErr != nil {
		errs = append(errs, fmt.Errorf("claim stale run lease %s: %w", runID, claimErr))
	}
	if claimErr == nil && !claimed {
		return nil
	}
	if claimErr == nil && claimed {
		defer s.releaseRunLease(context.WithoutCancel(ctx), runID)
	}

	runPatch := &agrunwrite.MutableRunView{}
	runPatch.SetId(runID)
	runPatch.SetStatus("failed")
	runPatch.SetErrorMessage(strings.TrimSpace(reason))
	runPatch.SetCompletedAt(now)
	if err := s.store.PatchRuns(ctx, []*agrunwrite.MutableRunView{runPatch}); err != nil {
		errs = append(errs, fmt.Errorf("patch stale run %s: %w", runID, err))
	}

	if row != nil {
		if err := s.patchScheduleResult(ctx, row, "failed", strings.TrimSpace(reason), timePtrUTC(now), false, now); err != nil {
			errs = append(errs, fmt.Errorf("patch schedule result %s: %w", strings.TrimSpace(row.Id), err))
		}
	}

	if conversationID := strings.TrimSpace(valueOrEmpty(run.ConversationId)); conversationID != "" {
		if err := s.cancelConversationAndMark(ctx, conversationID, "canceled"); err != nil {
			errs = append(errs, fmt.Errorf("cancel conversation %s: %w", conversationID, err))
		}
	}

	return errors.Join(errs...)
}

func (s *Service) tryClaimRunLease(ctx context.Context, runID string, now time.Time) (bool, error) {
	if s == nil || s.store == nil {
		return false, nil
	}
	runID = strings.TrimSpace(runID)
	owner := strings.TrimSpace(s.leaseOwner)
	if runID == "" || owner == "" {
		return false, nil
	}
	callCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), runLeaseCallTimeout)
	defer cancel()
	return s.store.TryClaimRun(callCtx, runID, owner, now.Add(s.leaseTTL))
}

func (s *Service) releaseRunLease(ctx context.Context, runID string) {
	if s == nil || s.store == nil {
		return
	}
	runID = strings.TrimSpace(runID)
	owner := strings.TrimSpace(s.leaseOwner)
	if runID == "" || owner == "" {
		return
	}
	callCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), runLeaseCallTimeout)
	defer cancel()
	if _, err := s.store.ReleaseRunLease(callCtx, runID, owner); err != nil {
		log.Printf("scheduler: release run lease run=%s owner=%s: %v", runID, owner, err)
	}
}

func (s *Service) startRunLeaseHeartbeat(ctx context.Context, runID string) func() {
	if s == nil || s.store == nil {
		return func() {}
	}
	runID = strings.TrimSpace(runID)
	owner := strings.TrimSpace(s.leaseOwner)
	if runID == "" || owner == "" {
		return func() {}
	}
	interval := s.leaseTTL / 2
	if interval < minRunHeartbeatEvery {
		interval = minRunHeartbeatEvery
	}
	heartbeatCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatCtx.Done():
				return
			case <-ticker.C:
				now := time.Now().UTC()
				claimed, err := s.tryClaimRunLease(heartbeatCtx, runID, now)
				if err != nil {
					log.Printf("scheduler: heartbeat run lease run=%s owner=%s: %v", runID, owner, err)
					continue
				}
				if !claimed {
					log.Printf("scheduler: heartbeat lost run lease run=%s owner=%s", runID, owner)
					return
				}
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}

func (s *Service) cancelConversationAndMark(ctx context.Context, conversationID, status string) error {
	if s == nil {
		return nil
	}
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return nil
	}
	errs := make([]error, 0, 2)
	if s.agent != nil {
		if err := s.agent.Terminate(context.WithoutCancel(ctx), conversationID); err != nil {
			errs = append(errs, fmt.Errorf("terminate conversation %s: %w", conversationID, err))
		}
	}
	if s.conversation != nil {
		if err := s.cancelConversationTreeAndMark(ctx, conversationID, strings.TrimSpace(status), map[string]struct{}{}); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (s *Service) cancelConversationTreeAndMark(ctx context.Context, conversationID, status string, visited map[string]struct{}) error {
	if s == nil || s.conversation == nil {
		return nil
	}
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return nil
	}
	if _, ok := visited[conversationID]; ok {
		return nil
	}
	visited[conversationID] = struct{}{}

	patchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()

	conv, err := s.conversation.GetConversation(
		patchCtx,
		conversationID,
		convcli.WithIncludeTranscript(true),
		convcli.WithIncludeToolCall(true),
		convcli.WithIncludeModelCall(true),
	)
	if err != nil || conv == nil {
		return err
	}

	errs := make([]error, 0, 4)
	convPatch := convcli.NewConversation()
	convPatch.SetId(conversationID)
	convPatch.SetStatus(status)
	if err := s.conversation.PatchConversations(patchCtx, convPatch); err != nil {
		errs = append(errs, fmt.Errorf("patch conversation status: %w", err))
	}
	if err := s.markConversationTermination(patchCtx, conv, status); err != nil {
		errs = append(errs, err)
	}

	for _, turn := range conv.GetTranscript() {
		if turn == nil {
			continue
		}
		for _, msg := range turn.Message {
			if msg == nil || msg.LinkedConversationId == nil {
				continue
			}
			childID := strings.TrimSpace(*msg.LinkedConversationId)
			if childID == "" {
				continue
			}
			if err := s.cancelConversationTreeAndMark(patchCtx, childID, status, visited); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

func (s *Service) markConversationTermination(ctx context.Context, conv *convcli.Conversation, status string) error {
	if s == nil || s.conversation == nil || conv == nil {
		return nil
	}
	now := time.Now().UTC()
	errs := make([]error, 0)

	for _, turn := range conv.GetTranscript() {
		if turn == nil {
			continue
		}
		if !isTerminalExecutionStatus(turn.Status) {
			upd := convcli.NewTurn()
			upd.SetId(strings.TrimSpace(turn.Id))
			upd.SetStatus(status)
			if err := s.conversation.PatchTurn(ctx, upd); err != nil {
				errs = append(errs, fmt.Errorf("patch turn %s: %w", strings.TrimSpace(turn.Id), err))
			}
		}

		if assistant := lastAssistantMessage(turn); assistant != nil {
			if assistant.Status == nil || !isTerminalExecutionStatus(*assistant.Status) {
				upd := convcli.NewMessage()
				upd.SetId(strings.TrimSpace(assistant.Id))
				upd.SetConversationID(strings.TrimSpace(assistant.ConversationId))
				if assistant.TurnId != nil && strings.TrimSpace(*assistant.TurnId) != "" {
					upd.SetTurnID(strings.TrimSpace(*assistant.TurnId))
				}
				upd.SetStatus(status)
				if err := s.conversation.PatchMessage(ctx, upd); err != nil {
					errs = append(errs, fmt.Errorf("patch message %s: %w", strings.TrimSpace(assistant.Id), err))
				}
			}
		}

		for _, msg := range turn.Message {
			if msg == nil {
				continue
			}
			if msg.ModelCall != nil && (msg.ModelCall.CompletedAt == nil || msg.ModelCall.CompletedAt.IsZero()) {
				upd := convcli.NewModelCall()
				upd.SetMessageID(strings.TrimSpace(msg.ModelCall.MessageId))
				if msg.ModelCall.TurnId != nil && strings.TrimSpace(*msg.ModelCall.TurnId) != "" {
					upd.SetTurnID(strings.TrimSpace(*msg.ModelCall.TurnId))
				}
				upd.SetStatus(modelCallFinalStatus(msg.ModelCall.Status))
				upd.SetCompletedAt(now)
				if err := s.conversation.PatchModelCall(ctx, upd); err != nil {
					errs = append(errs, fmt.Errorf("patch model call %s: %w", strings.TrimSpace(msg.ModelCall.MessageId), err))
				}
			}
			for _, toolMsg := range msg.ToolMessage {
				if toolMsg == nil || toolMsg.ToolCall == nil {
					continue
				}
				if toolMsg.ToolCall.CompletedAt != nil && !toolMsg.ToolCall.CompletedAt.IsZero() {
					continue
				}
				upd := convcli.NewToolCall()
				upd.SetMessageID(strings.TrimSpace(toolMsg.ToolCall.MessageId))
				upd.SetOpID(strings.TrimSpace(toolMsg.ToolCall.OpId))
				if toolMsg.ToolCall.TurnId != nil && strings.TrimSpace(*toolMsg.ToolCall.TurnId) != "" {
					upd.SetTurnID(strings.TrimSpace(*toolMsg.ToolCall.TurnId))
				}
				upd.SetStatus(toolCallFinalStatus(toolMsg.ToolCall.Status))
				upd.CompletedAt = timePtrUTC(now)
				upd.Has.CompletedAt = true
				if err := s.conversation.PatchToolCall(ctx, upd); err != nil {
					errs = append(errs, fmt.Errorf("patch tool call %s: %w", strings.TrimSpace(toolMsg.ToolCall.MessageId), err))
				}
			}
		}
	}

	return errors.Join(errs...)
}

func lastAssistantMessage(turn *convcli.Turn) *convcli.Message {
	if turn == nil || len(turn.Message) == 0 {
		return nil
	}
	for i := len(turn.Message) - 1; i >= 0; i-- {
		msg := turn.Message[i]
		if msg == nil {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(msg.Role), "assistant") {
			return (*convcli.Message)(msg)
		}
	}
	return nil
}

func modelCallFinalStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "failed", "error":
		return "failed"
	case "succeeded", "success", "completed", "done":
		return "succeeded"
	default:
		return "canceled"
	}
}

func toolCallFinalStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "failed", "error":
		return "failed"
	case "succeeded", "success", "completed", "done":
		return "succeeded"
	default:
		return "canceled"
	}
}

func isTerminalExecutionStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "canceled", "cancel", "failed", "error", "succeeded", "success", "completed", "done", "rejected":
		return true
	default:
		return false
	}
}

func (s *Service) patchScheduleResult(ctx context.Context, row *schedulepkg.ScheduleView, lastStatus string, lastError string, lastRunAt *time.Time, updateNext bool, now time.Time) error {
	if s == nil || row == nil {
		return nil
	}
	mut := &schedwrite.Schedule{}
	mut.SetId(row.Id)
	if lastRunAt != nil && !lastRunAt.IsZero() {
		mut.SetLastRunAt(lastRunAt.UTC())
	}
	if strings.TrimSpace(lastStatus) != "" {
		mut.SetLastStatus(strings.TrimSpace(lastStatus))
		if strings.EqualFold(strings.TrimSpace(lastStatus), "succeeded") && strings.TrimSpace(lastError) == "" {
			mut.LastError = nil
			mut.Has.LastError = true
		}
	}
	if strings.TrimSpace(lastError) != "" {
		mut.SetLastError(strings.TrimSpace(lastError))
	}
	if updateNext {
		if err := s.setNextRunAt(row, mut, now); err != nil {
			return err
		}
	}
	return s.store.PatchSchedule(ctx, mut)
}

func (s *Service) setNextRunAt(row *schedulepkg.ScheduleView, mut *schedwrite.Schedule, now time.Time) error {
	switch {
	case strings.EqualFold(strings.TrimSpace(row.ScheduleType), "cron") && row.CronExpr != nil && strings.TrimSpace(*row.CronExpr) != "":
		loc, _ := time.LoadLocation(strings.TrimSpace(row.Timezone))
		if loc == nil {
			loc = time.UTC
		}
		spec, err := parseCron(strings.TrimSpace(*row.CronExpr))
		if err != nil {
			return fmt.Errorf("invalid cron expr for schedule %s: %w", row.Id, err)
		}
		mut.SetNextRunAt(cronNext(spec, now.In(loc)).UTC())
	case row.IntervalSeconds != nil:
		mut.SetNextRunAt(now.Add(time.Duration(*row.IntervalSeconds) * time.Second).UTC())
	case strings.EqualFold(strings.TrimSpace(row.ScheduleType), "adhoc"):
		mut.NextRunAt = nil
		mut.Has.NextRunAt = true
	}
	return nil
}

func (s *Service) ensureLeaseConfig() {
	if s == nil {
		return
	}
	if v := strings.TrimSpace(os.Getenv("AGENTLY_SCHEDULER_LEASE_TTL")); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			s.leaseTTL = d
		}
	}
	if strings.TrimSpace(s.leaseOwner) != "" {
		return
	}
	if v := strings.TrimSpace(os.Getenv("AGENTLY_SCHEDULER_LEASE_OWNER")); v != "" {
		s.leaseOwner = v
		return
	}
	host, _ := os.Hostname()
	host = strings.TrimSpace(host)
	if host == "" {
		host = "unknown-host"
	}
	s.leaseOwner = fmt.Sprintf("%s:%d:%s", host, os.Getpid(), uuid.NewString())
}

// applyUserCred loads credentials from a scy secret URL and injects tokens
// into the context for downstream MCP tool calls.
func (s *Service) applyUserCred(ctx context.Context, credRef string) (context.Context, error) {
	if credRef == "" {
		return ctx, nil
	}
	return s.applyUserCredLegacyOOB(ctx, credRef)
}

func (s *Service) applyUserCredLegacyOOB(ctx context.Context, credRef string) (context.Context, error) {
	cfg := s.resolveUserCredAuthConfig()
	if cfg == nil {
		return ctx, fmt.Errorf("schedule user_cred_url requires auth.oauth configuration")
	}
	mode := strings.ToLower(strings.TrimSpace(cfg.Mode))
	if mode != "bff" {
		return ctx, fmt.Errorf("schedule user_cred_url requires auth.oauth.mode=bff")
	}
	cfgURL := strings.TrimSpace(cfg.ClientConfigURL)
	if cfgURL == "" {
		return ctx, fmt.Errorf("schedule user_cred_url requires auth.oauth.client.configURL")
	}

	cmd := &authorizer.Command{
		AuthFlow:   "OOB",
		UsePKCE:    true,
		SecretsURL: strings.TrimSpace(credRef),
		OAuthConfig: authorizer.OAuthConfig{
			ConfigURL: cfgURL,
		},
	}
	if scopes := cfg.Scopes; len(scopes) > 0 {
		cmd.Scopes = append([]string(nil), scopes...)
	} else {
		cmd.Scopes = []string{"openid"}
	}
	meta, userID := schedulerAuthMeta(ctx)
	logAuthf("schedule=%q run=%q user=%q user_cred authorize start ref_kind=%q scopes=%d",
		strings.TrimSpace(meta.ScheduleID),
		strings.TrimSpace(meta.ScheduleRunID),
		userID,
		userCredRefKind(credRef),
		len(cmd.Scopes),
	)

	authCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 1*time.Minute)
	defer cancel()
	oa := s.oauthAuthz
	if oa == nil {
		oa = authorizer.New()
	}
	oauthTok, err := oa.Authorize(authCtx, cmd)
	if err != nil {
		logAuthf("schedule=%q run=%q user=%q user_cred authorize failed ref_kind=%q err=%v",
			strings.TrimSpace(meta.ScheduleID),
			strings.TrimSpace(meta.ScheduleRunID),
			userID,
			userCredRefKind(credRef),
			err,
		)
		return ctx, fmt.Errorf("schedule user_cred authorize failed: %w", err)
	}
	if oauthTok == nil {
		return ctx, fmt.Errorf("schedule user_cred authorize returned empty token")
	}

	st := &scyauth.Token{Token: *oauthTok}
	st.PopulateIDToken()
	if s.tokenProvider != nil && strings.TrimSpace(st.RefreshToken) != "" {
		key := token.Key{Subject: credRef, Provider: "default"}
		_ = s.tokenProvider.Store(ctx, key, st)
		next, ensureErr := s.tokenProvider.EnsureTokens(ctx, key)
		if ensureErr == nil {
			logAuthf("schedule=%q run=%q user=%q user_cred authorize ok ref_kind=%q has_access=%t has_refresh=%t has_id=%t",
				strings.TrimSpace(meta.ScheduleID),
				strings.TrimSpace(meta.ScheduleRunID),
				userID,
				userCredRefKind(credRef),
				strings.TrimSpace(st.AccessToken) != "",
				strings.TrimSpace(st.RefreshToken) != "",
				strings.TrimSpace(st.IDToken) != "",
			)
			return next, nil
		}
		log.Printf("scheduler: ensure tokens after oob auth failed, using oauth token directly: %v", ensureErr)
	}
	logAuthf("schedule=%q run=%q user=%q user_cred authorize ok ref_kind=%q has_access=%t has_refresh=%t has_id=%t",
		strings.TrimSpace(meta.ScheduleID),
		strings.TrimSpace(meta.ScheduleRunID),
		userID,
		userCredRefKind(credRef),
		strings.TrimSpace(st.AccessToken) != "",
		strings.TrimSpace(st.RefreshToken) != "",
		strings.TrimSpace(st.IDToken) != "",
	)
	return s.withAuthTokens(ctx, st), nil
}

func (s *Service) withAuthTokens(ctx context.Context, tok *scyauth.Token) context.Context {
	if tok == nil {
		return ctx
	}
	ctx = iauth.WithTokens(ctx, tok)
	if v := strings.TrimSpace(tok.AccessToken); v != "" {
		ctx = iauth.WithBearer(ctx, v)
	}
	if v := strings.TrimSpace(tok.IDToken); v != "" {
		ctx = iauth.WithIDToken(ctx, v)
	}
	return ctx
}

func toPublicSchedule(row *schedulepkg.ScheduleView) *Schedule {
	if row == nil {
		return nil
	}
	return &Schedule{
		ID:              row.Id,
		Name:            row.Name,
		Description:     row.Description,
		CreatedByUserID: row.CreatedByUserId,
		Visibility:      row.Visibility,
		AgentRef:        row.AgentRef,
		ModelOverride:   row.ModelOverride,
		UserCredURL:     row.UserCredURL,
		Enabled:         row.Enabled,
		StartAt:         row.StartAt,
		EndAt:           row.EndAt,
		ScheduleType:    row.ScheduleType,
		CronExpr:        row.CronExpr,
		IntervalSeconds: row.IntervalSeconds,
		Timezone:        row.Timezone,
		TimeoutSeconds:  row.TimeoutSeconds,
		TaskPromptURI:   row.TaskPromptUri,
		TaskPrompt:      row.TaskPrompt,
		NextRunAt:       row.NextRunAt,
		LastRunAt:       row.LastRunAt,
		LastStatus:      row.LastStatus,
		LastError:       row.LastError,
		LeaseOwner:      row.LeaseOwner,
		LeaseUntil:      row.LeaseUntil,
		CreatedAt:       row.CreatedAt,
		UpdatedAt:       row.UpdatedAt,
	}
}

func toMutableSchedule(schedule *Schedule, isUpdate bool) *schedwrite.Schedule {
	now := time.Now().UTC()
	mut := &schedwrite.Schedule{}
	if id := strings.TrimSpace(schedule.ID); id != "" {
		mut.SetId(id)
	}
	if name := strings.TrimSpace(schedule.Name); name != "" {
		mut.SetName(name)
	}
	if schedule.Description != nil {
		mut.SetDescription(strings.TrimSpace(*schedule.Description))
	}
	if schedule.CreatedByUserID != nil && strings.TrimSpace(*schedule.CreatedByUserID) != "" {
		mut.SetCreatedByUserID(strings.TrimSpace(*schedule.CreatedByUserID))
	}
	if visibility := strings.TrimSpace(schedule.Visibility); visibility != "" {
		mut.SetVisibility(visibility)
	}
	if agentRef := strings.TrimSpace(schedule.AgentRef); agentRef != "" {
		mut.SetAgentRef(agentRef)
	}
	if schedule.ModelOverride != nil && strings.TrimSpace(*schedule.ModelOverride) != "" {
		mut.SetModelOverride(strings.TrimSpace(*schedule.ModelOverride))
	}
	if schedule.UserCredURL != nil && strings.TrimSpace(*schedule.UserCredURL) != "" {
		mut.SetUserCredURL(strings.TrimSpace(*schedule.UserCredURL))
	}
	if isUpdate || schedule.Enabled {
		mut.SetEnabled(schedule.Enabled)
	}
	if schedule.StartAt != nil && !schedule.StartAt.IsZero() {
		mut.SetStartAt(schedule.StartAt.UTC())
	}
	if schedule.EndAt != nil && !schedule.EndAt.IsZero() {
		mut.SetEndAt(schedule.EndAt.UTC())
	}
	if scheduleType := strings.TrimSpace(schedule.ScheduleType); scheduleType != "" {
		mut.SetScheduleType(scheduleType)
	}
	if schedule.CronExpr != nil && strings.TrimSpace(*schedule.CronExpr) != "" {
		mut.SetCronExpr(strings.TrimSpace(*schedule.CronExpr))
	}
	if schedule.IntervalSeconds != nil {
		mut.SetIntervalSeconds(*schedule.IntervalSeconds)
	}
	if timezone := strings.TrimSpace(schedule.Timezone); timezone != "" {
		mut.SetTimezone(timezone)
	}
	if isUpdate || schedule.TimeoutSeconds > 0 {
		mut.SetTimeoutSeconds(schedule.TimeoutSeconds)
	}
	if schedule.TaskPromptURI != nil && strings.TrimSpace(*schedule.TaskPromptURI) != "" {
		mut.SetTaskPromptUri(strings.TrimSpace(*schedule.TaskPromptURI))
	}
	if schedule.TaskPrompt != nil && strings.TrimSpace(*schedule.TaskPrompt) != "" {
		mut.SetTaskPrompt(strings.TrimSpace(*schedule.TaskPrompt))
	}
	if schedule.NextRunAt != nil && !schedule.NextRunAt.IsZero() {
		mut.SetNextRunAt(schedule.NextRunAt.UTC())
	}
	if schedule.LastRunAt != nil && !schedule.LastRunAt.IsZero() {
		mut.SetLastRunAt(schedule.LastRunAt.UTC())
	}
	if schedule.LastStatus != nil && strings.TrimSpace(*schedule.LastStatus) != "" {
		mut.SetLastStatus(strings.TrimSpace(*schedule.LastStatus))
	}
	if schedule.LastError != nil && strings.TrimSpace(*schedule.LastError) != "" {
		mut.SetLastError(strings.TrimSpace(*schedule.LastError))
	}
	if schedule.LeaseOwner != nil && strings.TrimSpace(*schedule.LeaseOwner) != "" {
		mut.SetLeaseOwner(strings.TrimSpace(*schedule.LeaseOwner))
	}
	if schedule.LeaseUntil != nil && !schedule.LeaseUntil.IsZero() {
		mut.SetLeaseUntil(schedule.LeaseUntil.UTC())
	}
	if !schedule.CreatedAt.IsZero() {
		mut.SetCreatedAt(schedule.CreatedAt.UTC())
	}
	mut.SetUpdatedAt(now)
	return mut
}

func schedulePrompt(row *schedulepkg.ScheduleView) string {
	if row == nil {
		return ""
	}
	if row.TaskPrompt != nil && strings.TrimSpace(*row.TaskPrompt) != "" {
		return strings.TrimSpace(*row.TaskPrompt)
	}
	if row.TaskPromptUri != nil {
		return strings.TrimSpace(*row.TaskPromptUri)
	}
	return ""
}

func scheduleUserID(ctx context.Context, row *schedulepkg.ScheduleView) string {
	userID := strings.TrimSpace(iauth.EffectiveUserID(ctx))
	if userID != "" {
		return userID
	}
	if row != nil && row.CreatedByUserId != nil {
		if owner := strings.TrimSpace(*row.CreatedByUserId); owner != "" {
			return owner
		}
	}
	return "system"
}

func hasActiveRun(runs []*schrun.RunView) bool {
	for _, run := range runs {
		if run == nil {
			continue
		}
		if run.CompletedAt == nil || run.CompletedAt.IsZero() {
			return true
		}
	}
	return false
}

func findCompletedRunForSlot(runs []*schrun.RunView, scheduledFor time.Time) *schrun.RunView {
	slot := scheduledFor.UTC()
	for _, run := range runs {
		if run == nil || run.ScheduledFor == nil || run.CompletedAt == nil || run.CompletedAt.IsZero() {
			continue
		}
		if run.ScheduledFor.UTC().Equal(slot) {
			return run
		}
	}
	return nil
}

func valueOrEmpty(v *string) string {
	if v == nil {
		return ""
	}
	return strings.TrimSpace(*v)
}

func timePtrUTC(t time.Time) *time.Time {
	u := t.UTC()
	return &u
}
