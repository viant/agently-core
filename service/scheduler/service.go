package scheduler

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	iauth "github.com/viant/agently-core/internal/auth"
	token "github.com/viant/agently-core/internal/auth/token"
	agentsvc "github.com/viant/agently-core/service/agent"
	"github.com/viant/scy"
	scyauth "github.com/viant/scy/auth"
	"golang.org/x/oauth2"
)

// Service manages schedule CRUD and execution of due schedules.
type Service struct {
	store         ScheduleStore
	agent         *agentsvc.Service
	interval      time.Duration
	tokenProvider token.Provider
	scyService    *scy.Service
}

// New creates a scheduler service.
func New(store ScheduleStore, agent *agentsvc.Service, opts ...Option) *Service {
	s := &Service{
		store:    store,
		agent:    agent,
		interval: 30 * time.Second,
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Option customises the scheduler service.
type Option func(*Service)

// WithInterval sets the watchdog polling interval.
func WithInterval(d time.Duration) Option {
	return func(s *Service) { s.interval = d }
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
	schedule, err := s.store.Get(id)
	if err != nil || schedule == nil {
		return schedule, err
	}
	if !isScheduleVisible(ctx, schedule) {
		return nil, nil
	}
	return schedule, nil
}

// List returns all schedules.
func (s *Service) List(ctx context.Context) ([]*Schedule, error) {
	list, err := s.store.List()
	if err != nil {
		return nil, err
	}
	filtered := make([]*Schedule, 0, len(list))
	for _, item := range list {
		if item == nil {
			continue
		}
		if isScheduleVisible(ctx, item) {
			filtered = append(filtered, item)
		}
	}
	return filtered, nil
}

// Upsert creates or updates a schedule.
func (s *Service) Upsert(ctx context.Context, schedule *Schedule) error {
	now := time.Now()
	if schedule.CreatedAt.IsZero() {
		schedule.CreatedAt = now
	}
	schedule.UpdatedAt = now
	if schedule.CreatedByUserID == nil {
		if userID := strings.TrimSpace(iauth.EffectiveUserID(ctx)); userID != "" {
			schedule.CreatedByUserID = &userID
		}
	}
	return s.store.Upsert(schedule)
}

// Delete removes a schedule.
func (s *Service) Delete(id string) error {
	return s.store.Delete(id)
}

// RunNow triggers immediate execution of a schedule.
func (s *Service) RunNow(ctx context.Context, id string) error {
	sched, err := s.store.Get(id)
	if err != nil {
		return fmt.Errorf("get schedule: %w", err)
	}
	if sched == nil {
		return fmt.Errorf("schedule %s not found", id)
	}
	return s.execute(ctx, sched)
}

// execute runs a single schedule.
func (s *Service) execute(ctx context.Context, sched *Schedule) error {
	if sched.UserCredURL != nil && *sched.UserCredURL != "" {
		var err error
		ctx, err = s.applyUserCred(ctx, *sched.UserCredURL)
		if err != nil {
			return fmt.Errorf("apply user cred for schedule %s: %w", sched.ID, err)
		}
	}

	prompt := ""
	if sched.TaskPrompt != nil {
		prompt = *sched.TaskPrompt
	}
	if prompt == "" {
		return fmt.Errorf("schedule %s has no task prompt", sched.ID)
	}

	// Determine the effective user ID: prefer context, fall back to schedule creator,
	// then fall back to "system" so unauthenticated / cron-triggered runs still work.
	userID := strings.TrimSpace(iauth.EffectiveUserID(ctx))
	if userID == "" && sched.CreatedByUserID != nil {
		userID = strings.TrimSpace(*sched.CreatedByUserID)
	}
	if userID == "" {
		userID = "system"
	}
	if strings.TrimSpace(iauth.EffectiveUserID(ctx)) == "" {
		ctx = iauth.WithUserInfo(ctx, &iauth.UserInfo{Subject: userID})
	}

	input := &agentsvc.QueryInput{
		AgentID:    sched.AgentRef,
		Query:      prompt,
		UserId:     userID,
		ScheduleId: sched.ID,
	}
	out := &agentsvc.QueryOutput{}
	if err := s.agent.Query(ctx, input, out); err != nil {
		return fmt.Errorf("execute schedule %s: %w", sched.ID, err)
	}

	now := time.Now()
	sched.LastRunAt = &now
	_ = s.store.Upsert(sched)
	return nil
}

// applyUserCred loads credentials from a scy secret URL and injects tokens
// into the context for downstream MCP tool calls.
func (s *Service) applyUserCred(ctx context.Context, credRef string) (context.Context, error) {
	if credRef == "" {
		return ctx, nil
	}

	// Load secret from scy resource URL.
	if s.scyService == nil {
		s.scyService = scy.New()
	}
	resource := scy.NewResource("", credRef, "")
	secret, err := s.scyService.Load(ctx, resource)
	if err != nil {
		return ctx, fmt.Errorf("load user cred from %s: %w", credRef, err)
	}

	// Extract token data from the loaded secret.
	var refreshToken, accessToken, idToken string
	if secret != nil {
		if tok, ok := secret.Target.(*scyauth.Token); ok && tok != nil {
			refreshToken = tok.RefreshToken
			accessToken = tok.AccessToken
			idToken = tok.IDToken
		}
	}

	// If we have a token provider, use it for full lifecycle management.
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

	// Fallback: inject whatever tokens we have directly.
	if accessToken != "" {
		ctx = iauth.WithBearer(ctx, accessToken)
	}
	if idToken != "" {
		ctx = iauth.WithIDToken(ctx, idToken)
	}
	return ctx, nil
}

// processDue finds and executes all due schedules.
func (s *Service) processDue(ctx context.Context) {
	due, err := s.store.ListDue(time.Now())
	if err != nil {
		log.Printf("scheduler: list due: %v", err)
		return
	}
	for _, sched := range due {
		if err := s.execute(ctx, sched); err != nil {
			log.Printf("scheduler: execute %s: %v", sched.ID, err)
		}
	}
}

func isScheduleVisible(ctx context.Context, schedule *Schedule) bool {
	if schedule == nil {
		return false
	}
	userID := strings.TrimSpace(iauth.EffectiveUserID(ctx))
	visibility := strings.TrimSpace(schedule.Visibility)
	if !strings.EqualFold(visibility, "private") {
		return true
	}
	if userID == "" || schedule.CreatedByUserID == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(*schedule.CreatedByUserID), userID)
}
