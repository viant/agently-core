package scheduler

import (
	"context"
	"errors"
	"strings"
	"time"

	convcli "github.com/viant/agently-core/app/store/conversation"
	token "github.com/viant/agently-core/internal/auth/token"
	schedulepkg "github.com/viant/agently-core/pkg/agently/scheduler/schedule"
	agentsvc "github.com/viant/agently-core/service/agent"
	svcauth "github.com/viant/agently-core/service/auth"
	"github.com/viant/scy"
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
	authCfg         *svcauth.Config
	users           svcauth.UserService
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

// Option customizes the scheduler service.
type Option func(*Service)

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
func WithAuthConfig(cfg *svcauth.Config) Option {
	return func(s *Service) { s.authCfg = cfg }
}

// WithUserService sets the user service used to resolve scheduler subjects to
// persistent user IDs for the created_by_user_id auth path.
func WithUserService(users svcauth.UserService) Option {
	return func(s *Service) { s.users = users }
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
		return errors.New("schedule is required")
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

// Delete removes a schedule when the caller owns it.
func (s *Service) Delete(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("schedule ID is required")
	}
	row, err := s.store.Get(ctx, id)
	if err != nil {
		return err
	}
	if row == nil {
		return errors.New("schedule " + id + " not found")
	}
	if row.CreatedByUserId != nil {
		owner := strings.TrimSpace(*row.CreatedByUserId)
		if owner != "" {
			userID := strings.TrimSpace(svcauth.EffectiveUserID(ctx))
			if userID == "" || userID != owner {
				return errors.New("schedule delete is only allowed for the owner")
			}
		}
	}
	return s.store.DeleteSchedule(ctx, id)
}

// RunNow enqueues and starts an immediate execution for a schedule.
func (s *Service) RunNow(ctx context.Context, id string) error {
	row, err := s.store.Get(ctx, id)
	if err != nil {
		return err
	}
	if row == nil {
		return errors.New("schedule " + id + " not found")
	}
	return s.enqueueAndLaunch(ctx, row, time.Now().UTC(), false)
}
