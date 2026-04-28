// Package agent coordinates an agent turn across multiple responsibilities
package agent

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/viant/afs"
	"github.com/viant/agently-core/app/executor/config"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	cancels "github.com/viant/agently-core/app/store/conversation/cancel"
	"github.com/viant/agently-core/app/store/data"
	token "github.com/viant/agently-core/internal/auth/token"
	implconv "github.com/viant/agently-core/internal/service/conversation"
	"github.com/viant/agently-core/protocol/agent"
	asynccfg "github.com/viant/agently-core/protocol/async"
	mcpmgr "github.com/viant/agently-core/protocol/mcp/manager"
	"github.com/viant/agently-core/protocol/tool"
	toolbundle "github.com/viant/agently-core/protocol/tool/bundle"
	"github.com/viant/agently-core/runtime/streaming"
	"github.com/viant/agently-core/service/augmenter"
	"github.com/viant/agently-core/service/core"
	elicitation "github.com/viant/agently-core/service/elicitation"
	elicrouter "github.com/viant/agently-core/service/elicitation/router"
	intakesvc "github.com/viant/agently-core/service/intake"
	"github.com/viant/agently-core/service/reactor"
	skillsvc "github.com/viant/agently-core/service/skill"
	tplrepo "github.com/viant/agently-core/workspace/repository/template"
	bundlerepo "github.com/viant/agently-core/workspace/repository/toolbundle"
)

// Option customises Service instances.
type Option func(*Service)

type Service struct {
	llm          *core.Service
	registry     tool.Registry
	fs           afs.Service
	agentFinder  agent.Finder
	augmenter    *augmenter.Service
	orchestrator *reactor.Service

	defaults *config.Defaults

	// conversation is a shared conversation client used to fetch transcript/usage.
	conversation apiconv.Client

	elicitation *elicitation.Service
	// Backward-compatible fields for wiring; passed into elicitation service
	elicRouter     elicrouter.ElicitationRouter
	awaiterFactory func() elicitation.Awaiter

	// Optional cancel registry used to expose per-turn cancel functions to
	// external actors (e.g., HTTP or UI) without creating multiple cancel scopes.
	cancelReg cancels.Registry

	// Optional MCP client manager to resolve resources via MCP servers.
	mcpMgr *mcpmgr.Manager

	// Optional provider for workspace tool bundles. When nil, bundles are derived
	// from current tool definitions (service-based).
	toolBundles func(ctx context.Context) ([]*toolbundle.Bundle, error)

	// Optional token provider for auth token lifecycle management.
	tokenProvider token.Provider

	// Optional data service for run record management (SecurityContext, resume).
	dataService data.Service

	// streamPub is the SSE bus publisher used to emit agent-level lifecycle
	// events such as turn_queued. Wired via SetElicitationStreamPublisher.
	streamPub streaming.Publisher

	asyncManager   *asynccfg.Manager
	asyncPollers   sync.Map
	bootstrapCache sync.Map

	relevanceSelector func(context.Context, relevanceSelectorInput) (*relevanceSelectorOutput, error)

	// intakeSvc runs the pre-turn intake sidecar when agent.Intake.Enabled is true.
	intakeSvc *intakesvc.Service
	skillSvc  *skillsvc.Service

	templateRepo *tplrepo.Repository

	runWorkerHost           string
	runLeaseOwner           string
	runHeartbeatIntervalSec int
	runHeartbeatEvery       time.Duration
}

func (s *Service) Finder() agent.Finder {
	return s.agentFinder
}

// AsyncManager returns the async Manager owned by this service. Exposed so
// bootstrap wiring (e.g. executor.Builder) can forward workspace-level
// `default.async` settings into `manager.StartGC` / narrator overrides
// without reaching through package-private fields.
func (s *Service) AsyncManager() *asynccfg.Manager {
	if s == nil {
		return nil
	}
	return s.asyncManager
}

func (s *Service) SetSkillService(svc *skillsvc.Service) {
	if s == nil {
		return
	}
	s.skillSvc = svc
}

// SetRuntime removed: orchestration decoupled

// WithElicitationRouter injects a router to coordinate elicitation waits
// for assistant-originated prompts. When set, the agent will register a
// waiter and block until the HTTP/UI handler completes the elicitation.
func WithElicitationRouter(r elicrouter.ElicitationRouter) Option {
	return func(s *Service) { s.elicRouter = r }
}

// WithNewElicitationAwaiter configures a local awaiter used to resolve
// assistant-originated elicitations in interactive environments (CLI).
func WithNewElicitationAwaiter(newAwaiter func() elicitation.Awaiter) Option {
	return func(s *Service) { s.awaiterFactory = newAwaiter }
}

// WithCancelRegistry injects a registry to register per-turn cancel functions
// when executing Agent.Query. When nil, cancel registration is skipped.
func WithCancelRegistry(reg cancels.Registry) Option {
	return func(s *Service) { s.cancelReg = reg }
}

// WithMCPManager attaches an MCP Manager to resolve resources via MCP servers.
func WithMCPManager(m *mcpmgr.Manager) Option { return func(s *Service) { s.mcpMgr = m } }

// WithToolBundles configures a provider returning global tool bundles.
func WithToolBundles(provider func(ctx context.Context) ([]*toolbundle.Bundle, error)) Option {
	return func(s *Service) { s.toolBundles = provider }
}

// WithTokenProvider injects a token provider for auth token lifecycle management.
func WithTokenProvider(p token.Provider) Option {
	return func(s *Service) { s.tokenProvider = p }
}

// WithDataService injects a data service for run record management.
func WithDataService(d data.Service) Option {
	return func(s *Service) { s.dataService = d }
}

// WithRelevanceSelector overrides the relevance selector used to populate
// ContextProjection.HiddenTurnIDs. Intended primarily for testing or custom
// selector integrations.
func WithRelevanceSelector(fn func(context.Context, relevanceSelectorInput) (*relevanceSelectorOutput, error)) Option {
	return func(s *Service) { s.relevanceSelector = fn }
}

// WithIntakeService injects the pre-turn intake sidecar service. When set,
// agents with Intake.Enabled=true will run the sidecar before the main turn.
func WithIntakeService(svc *intakesvc.Service) Option {
	return func(s *Service) { s.intakeSvc = svc }
}

func WithSkillService(svc *skillsvc.Service) Option {
	return func(s *Service) { s.skillSvc = svc }
}

// New creates a new agent service instance with the given tool registry.
func New(llm *core.Service, agentFinder agent.Finder, augmenter *augmenter.Service, registry tool.Registry,
	defaults *config.Defaults,
	convClient apiconv.Client,

	opts ...Option) *Service {
	srv := &Service{
		defaults:                defaults,
		llm:                     llm,
		agentFinder:             agentFinder,
		augmenter:               augmenter,
		registry:                registry,
		conversation:            convClient,
		fs:                      afs.New(),
		cancelReg:               cancels.Default(),
		asyncManager:            asynccfg.NewManager(),
		runHeartbeatIntervalSec: 60,
		runHeartbeatEvery:       30 * time.Second,
	}
	host, _ := os.Hostname()
	host = strings.TrimSpace(host)
	if host == "" {
		host = "unknown-host"
	}
	srv.runWorkerHost = host
	srv.runLeaseOwner = fmt.Sprintf("%s:%d:%s", host, os.Getpid(), uuid.NewString())

	for _, o := range opts {
		o(srv)
	}
	// Default workspace tool bundles provider if not injected.
	if srv.toolBundles == nil {
		repo := bundlerepo.New(afs.New())
		srv.toolBundles = func(ctx context.Context) ([]*toolbundle.Bundle, error) { return repo.LoadAll(ctx) }
	}
	if srv.templateRepo == nil {
		srv.templateRepo = tplrepo.New(afs.New())
	}
	// Instantiate default conversation API only when caller did not inject one.
	// Preserving injected clients is required for in-memory/e2e runtimes.
	if srv.conversation == nil {
		if dao, err := implconv.NewDatly(context.Background()); err == nil {
			if cli, err := implconv.New(context.Background(), dao); err == nil {
				srv.conversation = cli
			}
		}
	}
	if srv.asyncManager == nil {
		srv.asyncManager = asynccfg.NewManager()
	}
	// Wire core and orchestrator with conversation client
	if srv.conversation != nil && srv.llm != nil {
		srv.llm.SetConversationClient(srv.conversation)
	}

	// Initialize orchestrator with conversation client, agent finder, and a builder that mirrors runPlanLoop.
	srv.orchestrator = reactor.New(llm, registry, srv.conversation, srv.agentFinder,
		func(ctx context.Context, conv *apiconv.Conversation, instruction string) (*core.GenerateInput, error) {
			if conv == nil || srv.agentFinder == nil {
				return nil, fmt.Errorf("missing conversation or agent finder")
			}
			agentID, _, _, err := srv.resolveAgentIDForConversation(ctx, conv, "", strings.TrimSpace(instruction), "")
			if err != nil {
				return nil, fmt.Errorf("failed to resolve agent: %w", err)
			}
			ag, err := srv.loadResolvedAgent(ctx, agentID)
			if err != nil {
				return nil, fmt.Errorf("failed to find agent: %w", err)
			}
			qi := &QueryInput{
				Agent:          ag,
				ConversationID: conv.Id,
				Query:          strings.TrimSpace(instruction),
				DisplayQuery:   strings.TrimSpace(instruction),
				RequestTime:    time.Now(),
			}
			if isCapabilityAgentID(agentID) {
				autoTools := false
				qi.AutoSelectTools = &autoTools
				qi.ToolsAllowed = nil
				qi.DisableChains = true
			}
			// Ensure embedding model for knowledge matching like ensureEnvironment does
			if strings.TrimSpace(qi.EmbeddingModel) == "" && srv.defaults != nil {
				qi.EmbeddingModel = srv.defaults.Embedder
			}
			binding, bErr := srv.BuildBinding(ctx, qi)
			if bErr != nil {
				return nil, bErr
			}
			modelSel := ag.ModelSelection
			// Mirror runPlanLoop: allow an override; use conversation default as an effective override for this recovery run.
			if conv.DefaultModel != nil && strings.TrimSpace(*conv.DefaultModel) != "" {
				modelSel.Model = strings.TrimSpace(*conv.DefaultModel)
			} else if qi.ModelOverride != "" { // keep behavior consistent if override is ever provided
				modelSel.Model = qi.ModelOverride
			}
			genInput := &core.GenerateInput{
				Prompt:         ag.Prompt,
				SystemPrompt:   ag.SystemPrompt,
				Instruction:    ag.EffectiveInstructionPrompt(),
				Binding:        binding,
				ModelSelection: modelSel,
			}
			// Attribute participants as in runPlanLoop
			genInput.UserID = strings.TrimSpace(qi.UserId)
			genInput.AgentID = strings.TrimSpace(ag.ID)
			EnsureGenerateOptions(ctx, genInput, ag)
			// genInput.Options.Mode = "plan"
			return genInput, nil
		})

	srv.elicitation = elicitation.New(srv.conversation, nil, srv.elicRouter, srv.awaiterFactory)

	return srv
}

// SetElicitationStreamPublisher wires the streaming publisher into the agent's
// internal elicitation service so that LLM-generated elicitation events reach
// the SSE bus.
func (s *Service) SetElicitationStreamPublisher(p streaming.Publisher) {
	if s == nil {
		return
	}
	s.streamPub = p
	if s.elicitation != nil {
		s.elicitation.SetStreamPublisher(p)
	}
}

// ResolveElicitation applies an elicitation decision (accept/decline/cancel)
// and persists status/payload through the elicitation service.
func (s *Service) ResolveElicitation(ctx context.Context, conversationID, elicitationID, action string, payload map[string]interface{}) error {
	if s == nil || s.elicitation == nil {
		return fmt.Errorf("elicitation service not configured")
	}
	return s.elicitation.Resolve(ctx, conversationID, elicitationID, action, payload, "")
}
