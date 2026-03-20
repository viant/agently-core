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
	agentsvc "github.com/viant/agently-core/service/agent"
	"github.com/viant/scy"
	scyauth "github.com/viant/scy/auth"
	"golang.org/x/oauth2"
)

// Service manages persisted scheduler CRUD and execution of due schedules.
type Service struct {
	store         Store
	agent         *agentsvc.Service
	conversation  convcli.Client
	interval      time.Duration
	tokenProvider token.Provider
	scyService    *scy.Service
	leaseOwner    string
	leaseTTL      time.Duration
}

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
		if row == nil || !row.Enabled {
			continue
		}
		now := time.Now().UTC()
		due, scheduledFor, err := s.isDue(ctx, row, now)
		if err != nil {
			return started, err
		}
		if !due {
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

			runs, runErr := s.getRunsForDueCheck(ctx, row.Id, scheduledFor, row.NextRunAt != nil && !row.NextRunAt.IsZero())
			if runErr != nil {
				err = runErr
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

	runCtx := ctx
	if cred := strings.TrimSpace(valueOrEmpty(row.UserCredURL)); cred != "" {
		var err error
		runCtx, err = s.applyUserCred(runCtx, cred)
		if err != nil {
			log.Printf("scheduler: apply user cred schedule=%s run=%s: %v", row.Id, runID, err)
		}
	}

	userID := scheduleUserID(runCtx, row)
	if userID != "" && strings.TrimSpace(iauth.EffectiveUserID(runCtx)) == "" {
		runCtx = iauth.WithUserInfo(runCtx, &iauth.UserInfo{Subject: userID})
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
	err := s.agent.Query(runCtx, input, output)

	// Restore scheduler-specific run metadata because the interactive run path
	// currently defaults conversation_kind to "interactive".
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
	_, _ = s.store.ReleaseRunLease(context.Background(), runID, s.leaseOwner)
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

	if s.scyService == nil {
		s.scyService = scy.New()
	}
	resource := scy.NewResource("", credRef, "")
	secret, err := s.scyService.Load(ctx, resource)
	if err != nil {
		return ctx, fmt.Errorf("load user cred from %s: %w", credRef, err)
	}

	var refreshToken, accessToken, idToken string
	if secret != nil {
		if tok, ok := secret.Target.(*scyauth.Token); ok && tok != nil {
			refreshToken = tok.RefreshToken
			accessToken = tok.AccessToken
			idToken = tok.IDToken
		}
	}

	if s.tokenProvider != nil && refreshToken != "" {
		key := token.Key{Subject: credRef, Provider: "default"}
		_ = s.tokenProvider.Store(ctx, key, &scyauth.Token{
			Token: oauth2.Token{
				AccessToken:  accessToken,
				RefreshToken: refreshToken,
			},
			IDToken: idToken,
		})
		return s.tokenProvider.EnsureTokens(ctx, key)
	}

	if accessToken != "" {
		ctx = iauth.WithBearer(ctx, accessToken)
	}
	if idToken != "" {
		ctx = iauth.WithIDToken(ctx, idToken)
	}
	return ctx, nil
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
