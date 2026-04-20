package executor

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/viant/agently-core/app/executor/config"
	"github.com/viant/agently-core/app/store/conversation"
	cancels "github.com/viant/agently-core/app/store/conversation/cancel"
	"github.com/viant/agently-core/app/store/data"
	"github.com/viant/agently-core/genai/embedder"
	"github.com/viant/agently-core/genai/llm"
	token "github.com/viant/agently-core/internal/auth/token"
	convsvc "github.com/viant/agently-core/internal/service/conversation"
	agentmodel "github.com/viant/agently-core/protocol/agent"
	mcpclienthandler "github.com/viant/agently-core/protocol/mcp/clienthandler"
	mcpmgr "github.com/viant/agently-core/protocol/mcp/manager"
	"github.com/viant/agently-core/protocol/tool"
	llmagents "github.com/viant/agently-core/protocol/tool/service/llm/agents"
	promptsvc "github.com/viant/agently-core/protocol/tool/service/prompt"
	resourcessvc "github.com/viant/agently-core/protocol/tool/service/resources"
	"github.com/viant/agently-core/runtime/streaming"
	agentsvc "github.com/viant/agently-core/service/agent"
	"github.com/viant/agently-core/service/augmenter"
	svcauth "github.com/viant/agently-core/service/auth"
	callbacksvc "github.com/viant/agently-core/service/callback"
	"github.com/viant/agently-core/service/core"
	modelcallctx "github.com/viant/agently-core/service/core/modelcall"
	elicsvc "github.com/viant/agently-core/service/elicitation"
	elicrouter "github.com/viant/agently-core/service/elicitation/router"
	intakesvc "github.com/viant/agently-core/service/intake"
	skillsvc "github.com/viant/agently-core/service/skill"
	"github.com/viant/agently-core/workspace"
	"github.com/viant/agently-core/workspace/hotswap"
	embedderloader "github.com/viant/agently-core/workspace/loader/embedder"
	modelloader "github.com/viant/agently-core/workspace/loader/model"
	callbackrepo "github.com/viant/agently-core/workspace/repository/callback"
	promptrepo "github.com/viant/agently-core/workspace/repository/prompt"
	toolbundlerepo "github.com/viant/agently-core/workspace/repository/toolbundle"
	fsstore "github.com/viant/agently-core/workspace/store/fs"
	"github.com/viant/datly"
	protoclient "github.com/viant/mcp-protocol/client"
)

type Runtime struct {
	Defaults          *config.Defaults
	DAO               *datly.Service
	Conversation      conversation.Client
	Data              data.Service
	Registry          tool.Registry
	Core              *core.Service
	Agent             *agentsvc.Service
	MCPManager        *mcpmgr.Manager
	CancelRegistry    cancels.Registry
	ElicitationRouter elicrouter.ElicitationRouter
	Elicitation       *elicsvc.Service
	Streaming         streaming.Bus
	HotSwap           *hotswap.Manager
	Skills            *skillsvc.Service
	SkillWatcher      *skillsvc.Watcher
	CallbackDispatch  *callbacksvc.Service
	Store             workspace.Store
	KnowledgeStore    workspace.KnowledgeStore
	StateStore        workspace.StateStore

	// AuthConfig holds the auth configuration when auth is enabled.
	AuthConfig *svcauth.Config
	// AuthMiddleware is the HTTP middleware that extracts auth from requests.
	AuthMiddleware func(http.Handler) http.Handler

	// TokenProvider manages auth token lifecycle (cache, refresh, persistence).
	TokenProvider token.Provider
}

type Builder struct {
	defaults          *config.Defaults
	dao               *datly.Service
	conversation      conversation.Client
	data              data.Service
	registry          tool.Registry
	core              *core.Service
	agentSvc          *agentsvc.Service
	agentFinder       agentmodel.Finder
	agentLoader       agentmodel.Loader
	modelFinder       llm.Finder
	modelLoader       *modelloader.Service
	embedderFinder    embedder.Finder
	embedderLoader    *embedderloader.Service
	augmenter         *augmenter.Service
	mcpManager        *mcpmgr.Manager
	mcpAuthRTProvider mcpmgr.AuthRTProvider
	mcpJarProvider    mcpmgr.JarProvider
	mcpUserIDFn       mcpmgr.UserIDExtractor
	cancelRegistry    cancels.Registry
	elicRouter        elicrouter.ElicitationRouter
	streamPub         modelcallctx.StreamPublisher
	streamBus         streaming.Bus
	hotSwapEnabled    bool
	store             workspace.Store
	knowledgeStore    workspace.KnowledgeStore
	stateStore        workspace.StateStore
	tokenProvider     token.Provider
}

func NewBuilder() *Builder { return &Builder{} }

func (b *Builder) WithDefaults(v *config.Defaults) *Builder        { b.defaults = v; return b }
func (b *Builder) WithDAO(v *datly.Service) *Builder               { b.dao = v; return b }
func (b *Builder) WithConversation(v conversation.Client) *Builder { b.conversation = v; return b }
func (b *Builder) WithData(v data.Service) *Builder                { b.data = v; return b }
func (b *Builder) WithRegistry(v tool.Registry) *Builder           { b.registry = v; return b }
func (b *Builder) WithCore(v *core.Service) *Builder               { b.core = v; return b }
func (b *Builder) WithAgentService(v *agentsvc.Service) *Builder   { b.agentSvc = v; return b }
func (b *Builder) WithAgentFinder(v agentmodel.Finder) *Builder    { b.agentFinder = v; return b }
func (b *Builder) WithModelFinder(v llm.Finder) *Builder           { b.modelFinder = v; return b }
func (b *Builder) WithEmbedderFinder(v embedder.Finder) *Builder   { b.embedderFinder = v; return b }
func (b *Builder) WithAugmenter(v *augmenter.Service) *Builder     { b.augmenter = v; return b }
func (b *Builder) WithMCPManager(v *mcpmgr.Manager) *Builder       { b.mcpManager = v; return b }
func (b *Builder) WithMCPAuthRTProvider(v mcpmgr.AuthRTProvider) *Builder {
	b.mcpAuthRTProvider = v
	return b
}
func (b *Builder) WithMCPCookieJarProvider(v mcpmgr.JarProvider) *Builder {
	b.mcpJarProvider = v
	return b
}
func (b *Builder) WithMCPUserIDExtractor(v mcpmgr.UserIDExtractor) *Builder {
	b.mcpUserIDFn = v
	return b
}
func (b *Builder) WithCancelRegistry(v cancels.Registry) *Builder { b.cancelRegistry = v; return b }
func (b *Builder) WithElicitationRouter(v elicrouter.ElicitationRouter) *Builder {
	b.elicRouter = v
	return b
}
func (b *Builder) WithStreamPublisher(v modelcallctx.StreamPublisher) *Builder {
	b.streamPub = v
	return b
}
func (b *Builder) WithStreamingBus(v streaming.Bus) *Builder { b.streamBus = v; return b }
func (b *Builder) WithAgentLoader(v agentmodel.Loader) *Builder {
	b.agentLoader = v
	return b
}
func (b *Builder) WithModelLoader(v *modelloader.Service) *Builder {
	b.modelLoader = v
	return b
}
func (b *Builder) WithEmbedderLoader(v *embedderloader.Service) *Builder {
	b.embedderLoader = v
	return b
}
func (b *Builder) WithHotSwap(enabled bool) *Builder { b.hotSwapEnabled = enabled; return b }
func (b *Builder) WithStore(v workspace.Store) *Builder {
	b.store = v
	return b
}
func (b *Builder) WithKnowledgeStore(v workspace.KnowledgeStore) *Builder {
	b.knowledgeStore = v
	return b
}
func (b *Builder) WithStateStore(v workspace.StateStore) *Builder {
	b.stateStore = v
	return b
}
func (b *Builder) WithTokenProvider(v token.Provider) *Builder {
	b.tokenProvider = v
	return b
}

func (b *Builder) Build(ctx context.Context) (*Runtime, error) {
	if b.modelFinder == nil {
		return nil, errors.New("executor builder requires llm model finder")
	}
	if b.agentFinder == nil {
		return nil, errors.New("executor builder requires agent finder")
	}

	// Ensure store defaults.
	if b.store == nil {
		b.store = fsstore.New(workspace.Root())
	}
	if b.knowledgeStore == nil {
		b.knowledgeStore = fsstore.NewKnowledgeStore(workspace.RuntimeRoot())
	}
	if b.stateStore == nil {
		b.stateStore = fsstore.NewStateStore(workspace.StateRoot())
	}

	out := &Runtime{
		Defaults:       b.defaults,
		DAO:            b.dao,
		MCPManager:     b.mcpManager,
		Store:          b.store,
		KnowledgeStore: b.knowledgeStore,
		StateStore:     b.stateStore,
	}
	if out.Defaults == nil {
		out.Defaults = &config.Defaults{}
	}

	needsDAO := b.conversation == nil || b.data == nil
	if out.DAO == nil && needsDAO {
		var (
			dao *datly.Service
			err error
		)
		if strings.TrimSpace(os.Getenv("AGENTLY_DB_DSN")) == "" {
			dao, err = data.NewDatlyFromWorkspace(ctx, workspace.RuntimeRoot())
		} else {
			dao, err = data.NewDatly(ctx)
		}
		if err != nil {
			return nil, err
		}
		out.DAO = dao
	}
	if out.AuthConfig == nil {
		authCfg, err := svcauth.LoadWorkspaceConfig(workspace.Root())
		if err != nil {
			return nil, err
		}
		out.AuthConfig = authCfg
	}
	if b.tokenProvider == nil {
		b.tokenProvider = svcauth.NewCreatedByUserTokenProvider(out.AuthConfig, out.DAO)
	}

	out.Conversation = b.conversation
	if out.Conversation == nil {
		cli, err := convsvc.New(ctx, out.DAO)
		if err != nil {
			return nil, err
		}
		out.Conversation = cli
	}

	out.Data = b.data
	if out.Data == nil {
		out.Data = data.NewService(out.DAO)
	}

	out.ElicitationRouter = b.elicRouter
	if out.ElicitationRouter == nil {
		out.ElicitationRouter = elicrouter.New()
	}

	out.Elicitation = elicsvc.New(out.Conversation, nil, out.ElicitationRouter, nil)

	out.MCPManager = b.mcpManager
	if out.MCPManager == nil {
		mgr, err := b.newDefaultMCPManager(out.Conversation, out.Elicitation)
		if err != nil {
			return nil, err
		}
		out.MCPManager = mgr
	}

	out.Registry = b.registry
	if out.Registry == nil {
		reg, err := tool.NewDefaultRegistry(out.MCPManager)
		if err != nil {
			return nil, err
		}
		out.Registry = reg
	}
	if !shouldSkipRegistryInitialize() {
		out.Registry.Initialize(ctx)
	}
	skillsvc.ExecFn = out.Registry.Execute
	out.Skills = skillsvc.New(out.Defaults, out.Conversation, b.agentFinder)
	if err := out.Skills.Load(ctx); err != nil {
		return nil, err
	}
	out.SkillWatcher = skillsvc.NewWatcher(out.Skills)
	if err := out.SkillWatcher.Start(ctx); err != nil {
		return nil, err
	}

	out.Core = b.core
	if out.Core == nil {
		out.Core = core.New(b.modelFinder, out.Registry, out.Conversation)
	}

	aug := b.augmenter
	if aug == nil {
		opts := []func(*augmenter.Service){}
		if out.MCPManager != nil {
			opts = append(opts, augmenter.WithMCPManager(out.MCPManager))
		}
		aug = augmenter.New(b.embedderFinder, opts...)
	}

	out.CancelRegistry = b.cancelRegistry
	if out.CancelRegistry == nil {
		out.CancelRegistry = cancels.Default()
	}
	out.Streaming = b.streamBus
	if out.Streaming == nil {
		out.Streaming = streaming.NewMemoryBus(0)
	}
	if out.Skills != nil {
		out.Skills.SetStreamPublisher(out.Streaming)
	}
	if publisherSetter, ok := out.Conversation.(interface{ SetStreamPublisher(streaming.Publisher) }); ok {
		publisherSetter.SetStreamPublisher(out.Streaming)
	}
	streamPub := b.streamPub
	if streamPub == nil {
		streamPub = newStreamPublisherAdapter(out.Streaming)
	}
	if streamPub != nil {
		out.Core.SetStreamPublisher(streamPub)
	}
	if out.Elicitation != nil {
		out.Elicitation.SetStreamPublisher(out.Streaming)
	}

	out.Agent = b.agentSvc
	if out.Agent == nil {
		agentOpts := []agentsvc.Option{agentsvc.WithCancelRegistry(out.CancelRegistry)}
		if out.ElicitationRouter != nil {
			agentOpts = append(agentOpts, agentsvc.WithElicitationRouter(out.ElicitationRouter))
		}
		if out.MCPManager != nil {
			agentOpts = append(agentOpts, agentsvc.WithMCPManager(out.MCPManager))
		}
		if b.tokenProvider != nil {
			agentOpts = append(agentOpts, agentsvc.WithTokenProvider(b.tokenProvider))
		}
		if out.Data != nil {
			agentOpts = append(agentOpts, agentsvc.WithDataService(out.Data))
		}
		promptRepo := promptrepo.NewWithStore(out.Store)
		bundleRepo := toolbundlerepo.NewWithStore(out.Store)
		intakeSvc := intakesvc.New(out.Core,
			intakesvc.WithProfileRepo(promptRepo),
			intakesvc.WithBundleRepo(bundleRepo),
		)
		agentOpts = append(agentOpts, agentsvc.WithIntakeService(intakeSvc))
		agentOpts = append(agentOpts, agentsvc.WithSkillService(out.Skills))
		out.Agent = agentsvc.New(out.Core, b.agentFinder, aug, out.Registry, out.Defaults, out.Conversation, agentOpts...)
	}
	if out.Agent != nil {
		out.Agent.SetSkillService(out.Skills)
	}
	if out.Skills != nil {
		if err := tool.AddInternalService(out.Registry, out.Skills); err != nil {
			return nil, err
		}
	}
	if resourcesSvc := resourcessvc.New(aug,
		resourcessvc.WithMCPManager(out.MCPManager),
		resourcessvc.WithConversationClient(out.Conversation),
		resourcessvc.WithAgentFinder(b.agentFinder),
		resourcessvc.WithDefaultEmbedder(out.Defaults.Embedder),
		resourcessvc.WithSkillService(out.Skills),
	); resourcesSvc != nil {
		if err := tool.AddInternalService(out.Registry, resourcesSvc); err != nil {
			return nil, err
		}
	}
	promptRepo := promptrepo.NewWithStore(out.Store)
	if err := tool.AddInternalService(out.Registry, llmagents.New(out.Agent,
		llmagents.WithConversationClient(out.Conversation),
		llmagents.WithPromptRepo(promptRepo),
		llmagents.WithMCPManager(out.MCPManager),
		llmagents.WithModelFinder(b.modelFinder),
	)); err != nil {
		return nil, err
	}
	{
		promptSvc := promptsvc.New(promptRepo,
			promptsvc.WithConversationClient(out.Conversation),
			promptsvc.WithAgentFinder(b.agentFinder),
			promptsvc.WithMCPManager(out.MCPManager),
		)
		if err := tool.AddInternalService(out.Registry, promptSvc); err != nil {
			return nil, err
		}
	}
	// Wire the streaming bus into the agent's internal elicitation service so
	// LLM-generated elicitation events reach the SSE channel.
	if out.Agent != nil && out.Streaming != nil {
		out.Agent.SetElicitationStreamPublisher(out.Streaming)
	}

	out.TokenProvider = b.tokenProvider

	// Callback dispatcher — declarative forge-submit → tool routing driven by
	// `<workspace>/callbacks/*.yaml`. Optional: when the workspace has no
	// callbacks directory, the service is still constructed but every
	// dispatch returns "no callback registered".
	if out.Registry != nil {
		callbackRepo := callbackrepo.NewWithStore(out.Store)
		cbOpts := []callbacksvc.Option{}
		if out.Conversation != nil {
			cbOpts = append(cbOpts, callbacksvc.WithConversationClient(out.Conversation))
		}
		out.CallbackDispatch = callbacksvc.New(callbackRepo, out.Registry, cbOpts...)
	}

	if b.hotSwapEnabled {
		mgr, err := initHotSwap(ctx, b)
		if err != nil {
			return nil, err
		}
		out.HotSwap = mgr
	}
	return out, nil
}

func shouldSkipRegistryInitialize() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("AGENTLY_SKIP_REGISTRY_INIT"))) {
	case "1", "true", "yes", "y", "on":
		return true
	}
	return false
}

func (b *Builder) newDefaultMCPManager(conv conversation.Client, elicitation *elicsvc.Service) (*mcpmgr.Manager, error) {
	if conv == nil || elicitation == nil {
		return nil, fmt.Errorf("executor builder requires conversation and elicitation service for default MCP manager")
	}
	opts := []mcpmgr.Option{
		mcpmgr.WithHandlerFactory(func() protoclient.Handler {
			return mcpclienthandler.New(elicitation, conv)
		}),
	}
	if b.mcpAuthRTProvider != nil {
		opts = append(opts, mcpmgr.WithAuthRoundTripperProvider(b.mcpAuthRTProvider))
	}
	if b.mcpJarProvider != nil {
		opts = append(opts, mcpmgr.WithCookieJarProvider(b.mcpJarProvider))
	}
	if b.mcpUserIDFn != nil {
		opts = append(opts, mcpmgr.WithUserIDExtractor(b.mcpUserIDFn))
	}
	if b.tokenProvider != nil {
		opts = append(opts, mcpmgr.WithTokenProvider(b.tokenProvider))
	}
	return mcpmgr.New(mcpmgr.NewRepoProvider(), opts...)
}

func (r *Runtime) IsReady() bool {
	return r != nil &&
		r.Agent != nil &&
		r.Core != nil &&
		r.Registry != nil &&
		r.Conversation != nil &&
		r.Data != nil &&
		r.Defaults != nil
}

func (r *Runtime) DefaultModel() string {
	if r == nil || r.Defaults == nil {
		return ""
	}
	return strings.TrimSpace(r.Defaults.Model)
}
