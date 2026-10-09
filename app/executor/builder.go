package executor

import (
	"context"
	"errors"
	"fmt"
	"github.com/viant/agently-core/protocol/primitive"
	"github.com/viant/agently-core/service/reporting/catalog"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/viant/afs"
	afsscratchpad "github.com/viant/afs/scratchpad"
	"github.com/viant/agently-core/app/executor/config"
	"github.com/viant/agently-core/app/store/conversation"
	cancels "github.com/viant/agently-core/app/store/conversation/cancel"
	"github.com/viant/agently-core/app/store/data"
	reportstore "github.com/viant/agently-core/app/store/reporting"
	reportfs "github.com/viant/agently-core/app/store/reporting/fs"
	reportsql "github.com/viant/agently-core/app/store/reporting/sql"
	"github.com/viant/agently-core/genai/embedder"
	"github.com/viant/agently-core/genai/llm"
	token "github.com/viant/agently-core/internal/auth/token"
	convsvc "github.com/viant/agently-core/internal/service/conversation"
	executionprotection "github.com/viant/agently-core/internal/tool/executionprotection"
	agentmodel "github.com/viant/agently-core/protocol/agent"
	dsproto "github.com/viant/agently-core/protocol/datasource"
	mcpclienthandler "github.com/viant/agently-core/protocol/mcp/clienthandler"
	mcpmgr "github.com/viant/agently-core/protocol/mcp/manager"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/agently-core/protocol/tool"
	llmagents "github.com/viant/agently-core/protocol/tool/service/llm/agents"
	promptsvc "github.com/viant/agently-core/protocol/tool/service/prompt"
	resourcessvc "github.com/viant/agently-core/protocol/tool/service/resources"
	scratchpadsvc "github.com/viant/agently-core/protocol/tool/service/scratchpad"
	windowprotocol "github.com/viant/agently-core/protocol/window"
	"github.com/viant/agently-core/runtime/streaming"
	agentsvc "github.com/viant/agently-core/service/agent"
	"github.com/viant/agently-core/service/augmenter"
	svcauth "github.com/viant/agently-core/service/auth"
	callbacksvc "github.com/viant/agently-core/service/callback"
	"github.com/viant/agently-core/service/core"
	modelcallctx "github.com/viant/agently-core/service/core/modelcall"
	dssvc "github.com/viant/agently-core/service/datasource"
	elicsvc "github.com/viant/agently-core/service/elicitation"
	elicrouter "github.com/viant/agently-core/service/elicitation/router"
	goalsys "github.com/viant/agently-core/service/goal"
	intakesvc "github.com/viant/agently-core/service/intake"
	policy "github.com/viant/agently-core/service/policy"
	forgeuisvc "github.com/viant/agently-core/service/primitiveprovider"
	reportingsvc "github.com/viant/agently-core/service/reporting"
	reportingrunsvc "github.com/viant/agently-core/service/reportingrun"
	resourcesvc "github.com/viant/agently-core/service/resource"
	skillsvc "github.com/viant/agently-core/service/skill"
	"github.com/viant/agently-core/service/ui/permittedview"
	"github.com/viant/agently-core/workspace"
	wscfg "github.com/viant/agently-core/workspace/config"
	"github.com/viant/agently-core/workspace/hotswap"
	embedderloader "github.com/viant/agently-core/workspace/loader/embedder"
	modelloader "github.com/viant/agently-core/workspace/loader/model"
	callbackrepo "github.com/viant/agently-core/workspace/repository/callback"
	intakerepo "github.com/viant/agently-core/workspace/repository/intake"
	tplrepo "github.com/viant/agently-core/workspace/repository/template"
	toolbundlerepo "github.com/viant/agently-core/workspace/repository/toolbundle"
	fsstore "github.com/viant/agently-core/workspace/store/fs"
	"github.com/viant/authz"
	"github.com/viant/datly/standalone"
	forgetypes "github.com/viant/forge/backend/types"
	protoclient "github.com/viant/mcp-protocol/client"
)

type Runtime struct {
	ExecutionContext         func(context.Context) context.Context
	registryWarmupMu         sync.Mutex
	registryWarmupCancel     context.CancelFunc
	registryWarmupDone       chan struct{}
	registryRefreshDone      chan struct{}
	closed                   bool
	ownedNative              *standalone.Server
	ownedAugmenter           *augmenter.Service
	ownedReportingCancel     context.CancelFunc
	ownedReportingWorker     *reportingsvc.Worker
	closeOnce                sync.Once
	closeError               error
	RawResourceBoundary      *workspace.RawResourceBoundary
	ComponentDispatcher      windowprotocol.ComponentDispatcher
	ComponentAuthority       policy.AuthoritySnapshotResolver
	PrimitiveProviders       *resourcesvc.Gateway
	PrimitiveWindows         *resourcesvc.CompositeWindowCatalog
	LocalPrimitiveProvider   *resourcesvc.LocalProvider
	localPrimitiveRegistered bool
	Defaults                 *config.Defaults
	// AuthorizationTool is the workspace-configured MCP adapter for permitted
	// Forge views.
	AuthorizationTool              string
	AuthorizationPolicy            *policy.Runtime
	WindowAuthorizer               func(context.Context, string) (bool, error)
	PermittedResolver              permittedview.Resolver
	DatasourceAuthorizer           func(context.Context, string, map[string]interface{}) error
	DatasourceResourceRevalidator  dssvc.ResourceRevalidator
	DatasourceDefinitionAuthorizer dssvc.DefinitionAuthorizer
	DatasourceDefinitionResolver   dssvc.ResourceDefinitionResolver
	DatasourceDisableCache         bool
	ReportAuthorizer               func(context.Context, string, string) error
	ToolAuthorizer                 func(context.Context, string, map[string]interface{}) error
	Native                         *standalone.Server
	Conversation                   conversation.Client
	Data                           data.Service
	Registry                       tool.Registry
	Core                           *core.Service
	Augmenter                      *augmenter.Service
	Agent                          *agentsvc.Service
	MCPManager                     *mcpmgr.Manager
	CancelRegistry                 cancels.Registry
	ElicitationRouter              elicrouter.ElicitationRouter
	Elicitation                    *elicsvc.Service
	Streaming                      streaming.Bus
	HotSwap                        *hotswap.Manager
	Skills                         *skillsvc.Service
	SkillWatcher                   *skillsvc.Watcher
	CallbackDispatch               *callbacksvc.Service
	Reporting                      *reportingsvc.Service
	ReportingClient                reportstore.Client // host-owned persistence lookup for dynamic authorization mappings
	ReportRuns                     *reportingrunsvc.Service
	// GoalStore retains the resolved injected or native store for agent/tool sharing.
	GoalStore       goalsys.Store
	ReportingWorker *reportingsvc.Worker
	Store           workspace.Store
	KnowledgeStore  workspace.KnowledgeStore
	StateStore      workspace.StateStore
	// UIBridge is the single Forge UI service shared by browser RPC, agents,
	// and all UI-facing internal tools for this runtime.
	UIBridge *forgeuisvc.Service

	// AuthConfig holds the auth configuration when auth is enabled.
	AuthConfig *svcauth.Config
	// AuthMiddleware is the HTTP middleware that extracts auth from requests.
	AuthMiddleware func(http.Handler) http.Handler

	// TokenProvider manages auth token lifecycle (cache, refresh, persistence).
	TokenProvider token.Provider
}

const defaultReportingQueueIntervalMs = 250

const defaultReportingStoreConnectorRef = "agently"

func resolveScratchpadTemplate() string {
	template := strings.TrimSpace(os.Getenv(scratchpadsvc.EnvScratchpadURI))
	if template != "" {
		return template
	}
	return scratchpadsvc.DefaultRootURITemplate
}

type Builder struct {
	nativeComponentServices      []string
	componentDispatcher          windowprotocol.ComponentDispatcher
	reportCatalog                catalog.Provider
	reportResourceResolver       reportingsvc.ResourceResolver
	reportResourceService        primitive.ResourceAuthoring
	authorizationProviders       map[string]AuthorizationProvider
	capabilityMappings           map[string]permittedview.CapabilityMapping
	capabilityMappingsV2         map[string]permittedview.CapabilityMappingV2
	uiBridge                     *forgeuisvc.Service
	defaults                     *config.Defaults
	conversation                 conversation.Client
	data                         data.Service
	goalStore                    goalsys.Store
	skipRegistryInitialize       bool
	native                       *standalone.Server
	registry                     tool.Registry
	core                         *core.Service
	agentSvc                     *agentsvc.Service
	agentFinder                  agentmodel.Finder
	agentLoader                  agentmodel.Loader
	modelFinder                  llm.Finder
	modelLoader                  *modelloader.Service
	embedderFinder               embedder.Finder
	embedderLoader               *embedderloader.Service
	augmenter                    *augmenter.Service
	mcpManager                   *mcpmgr.Manager
	primitiveActor               resourcesvc.ActorResolver
	primitiveVerifier            resourcesvc.ActorVerifier
	primitiveLocalIdentity       string
	primitiveGatewayIdentity     string
	primitiveWindowAdmission     resourcesvc.WindowAdmission
	windowOpenBootstrap          permittedview.OpenBootstrap
	windowOpenSelection          permittedview.OpenSelectionCheck
	primitiveDatasourceAdmission func(context.Context, identity.ResolvedResource, *dsproto.DataSource, map[string]interface{}) error
	primitiveTargetProof         forgetypes.WindowTargetProof
	localPrimitiveProvider       *resourcesvc.LocalProvider
	mcpAuthRTProvider            mcpmgr.AuthRTProvider
	mcpJarProvider               mcpmgr.JarProvider
	mcpUserIDFn                  mcpmgr.UserIDExtractor
	cancelRegistry               cancels.Registry
	elicRouter                   elicrouter.ElicitationRouter
	streamPub                    modelcallctx.StreamPublisher
	streamBus                    streaming.Bus
	hotSwapEnabled               bool
	store                        workspace.Store
	knowledgeStore               workspace.KnowledgeStore
	stateStore                   workspace.StateStore
	tokenProvider                token.Provider
	reportingService             *reportingsvc.Service
	mcpDelegatedAuth             *svcauth.DelegatedMCPAuth
}

// AuthorizationProvider is registered by the host, not workspace YAML.
type AuthorizationProvider struct {
	WindowReadDecisionScope       forgeuisvc.WindowReadDecisionScope
	ExecutionContext              func(context.Context) context.Context
	AuthoritySnapshot             policy.AuthoritySnapshotResolver
	ComponentAuthoritySnapshot    policy.AuthoritySnapshotResolver
	DatasourceResourceRevalidator dssvc.ResourceRevalidator
	DatasourceDefinitionAuthorize dssvc.DefinitionAuthorizer
	DatasourceDefinitionResolver  dssvc.ResourceDefinitionResolver
	Service                       *authz.Service
	Account                       func(context.Context, authz.Facts) (string, error)
	AuthorityRevision             func(context.Context, authz.Facts, string) (string, time.Time, error)
	AccountProjection             permittedview.AccountProjection
	EntityPermission              func(context.Context, authz.Facts, authz.Entity, string) (bool, error)
	EntityPermissionWithLease     func(context.Context, authz.Facts, authz.Entity, string) (bool, time.Time, error)
	EntityRoles                   func(context.Context, authz.Facts, authz.Entity) ([]string, error)
	Gate                          policy.GateCheck
	GateEvaluator                 policy.EvaluatorBridge
	DatasourceAuthorize           func(context.Context, string, map[string]interface{}) error
	ReportAuthorize               func(context.Context, string, string) error
	ToolAuthorize                 func(context.Context, string, map[string]interface{}) error
	PolicyVersion                 string
	PolicyResource                func(context.Context, string, policy.Candidate) (authz.Resource, string, error)
	WindowAuthorize               func(context.Context, string) (bool, error)
}

func (b *Builder) WithAuthorizationProvider(ref string, provider AuthorizationProvider) *Builder {
	if provider.Gate == nil {
		provider.Gate = policy.GateFromEvaluator(provider.GateEvaluator)
	}
	if b.authorizationProviders == nil {
		b.authorizationProviders = map[string]AuthorizationProvider{}
	}
	b.authorizationProviders[ref] = provider
	return b
}

func (b *Builder) WithCapabilityMapping(ref string, mapping permittedview.CapabilityMapping) *Builder {
	if b.capabilityMappings == nil {
		b.capabilityMappings = map[string]permittedview.CapabilityMapping{}
	}
	b.capabilityMappings[ref] = mapping
	return b
}

func (b *Builder) WithCapabilityMappingV2(ref string, mapping permittedview.CapabilityMappingV2) *Builder {
	if b.capabilityMappingsV2 == nil {
		b.capabilityMappingsV2 = map[string]permittedview.CapabilityMappingV2{}
	}
	b.capabilityMappingsV2[ref] = mapping
	return b
}

// WithUIBridge supplies a host-configured Forge bridge. Authz window mode
// requires its catalog to report a host-installed admission callback.
func (b *Builder) WithUIBridge(bridge *forgeuisvc.Service) *Builder {
	b.uiBridge = bridge
	return b
}

func resolveReportingStoreDefaults(defaults *config.Defaults) config.ReportingStoreDefaults {
	if defaults == nil {
		return config.ReportingStoreDefaults{}
	}
	store := defaults.Reporting.Store
	backend := strings.TrimSpace(store.Backend)
	connectorRef := strings.TrimSpace(store.ConnectorRef)
	if backend == "" && hasReportingDBConfigEnv() {
		backend = "sql"
	}
	if strings.EqualFold(backend, "sql") && connectorRef == "" {
		connectorRef = defaultReportingStoreConnectorRef
	}
	return config.ReportingStoreDefaults{
		Backend:      backend,
		ConnectorRef: connectorRef,
	}
}

func reportingSQLStoreEnabled(defaults *config.Defaults) bool {
	if defaults == nil || !defaults.Reporting.Enabled {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(resolveReportingStoreDefaults(defaults).Backend), "sql")
}

// hasReportingDBConfigEnv belongs at the Agently runtime layer, not Forge.
// Forge consumes reporting services/contracts; Agently-core decides whether
// report persistence should auto-bind to the local runtime database contract.
func hasReportingDBConfigEnv() bool {
	for _, envKey := range []string{
		"AGENTLY_DB_DSN",
		"AGENTLY_DB_PATH",
		"AGENTLY_DB_DRIVER",
		"AGENTLY_DB_SECRETS",
	} {
		if strings.TrimSpace(os.Getenv(envKey)) != "" {
			return true
		}
	}
	return false
}

func NewBuilder() *Builder { return &Builder{} }

func (b *Builder) WithDefaults(v *config.Defaults) *Builder { b.defaults = v; return b }

// WithSkipRegistryInitialize lets a host own asynchronous registry warmup.
// Default builders retain synchronous initialization and the environment override.
func (b *Builder) WithSkipRegistryInitialize(skip bool) *Builder {
	b.skipRegistryInitialize = skip
	return b
}

// WithGoalStore supplies a domain store using the shared Datly 1.0 invoker.
func (b *Builder) WithGoalStore(store goalsys.Store) *Builder {
	b.goalStore = store
	return b
}

// WithNativeRuntime supplies the one linked Datly runtime shared by migrated
// application callers. Its connector pools and shutdown belong to the caller.
func (b *Builder) WithNativeRuntime(server *standalone.Server) *Builder {
	b.native = server
	return b
}

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

// WithPrimitiveAuthority registers verified host identity for delegated
// discovery. Namespace restrictions belong to MCP connections, not here.
func (b *Builder) WithPrimitiveAuthority(actor resourcesvc.ActorResolver, verify resourcesvc.ActorVerifier, localProviderIdentity string) *Builder {
	b.primitiveActor, b.primitiveVerifier, b.primitiveLocalIdentity = actor, verify, localProviderIdentity
	return b
}

// WithPrimitiveGatewayIdentity excludes an aggregate/self proxy identity.
// The distinct local authoritative YAML source may still be a provider.
func (b *Builder) WithPrimitiveGatewayIdentity(identity string) *Builder {
	b.primitiveGatewayIdentity = identity
	return b
}

// WithPrimitiveWindows enables delegated windows on the existing host open
// path. Provider namespaces and resources are discovered from MCP connections.
// WithWindowOpenBootstrap supplies explicit protected row provenance and selected
// object matching. It never infers a datasource from UI rendering hints.
func (b *Builder) WithWindowOpenBootstrap(load permittedview.OpenBootstrap, match permittedview.OpenSelectionCheck) *Builder {
	b.windowOpenBootstrap, b.windowOpenSelection = load, match
	return b
}

func (b *Builder) WithPrimitiveWindows(admit resourcesvc.WindowAdmission, proof forgetypes.WindowTargetProof) *Builder {
	b.primitiveWindowAdmission, b.primitiveTargetProof = admit, proof
	return b
}
func (b *Builder) WithPrimitiveDatasourceAdmission(admit func(context.Context, identity.ResolvedResource, *dsproto.DataSource, map[string]interface{}) error) *Builder {
	b.primitiveDatasourceAdmission = admit
	return b
}

// WithLocalPrimitiveProvider installs the local authoritative YAML provider.
// The MCP server exposes it separately from federated registry tools.
func (b *Builder) WithLocalPrimitiveProvider(provider *resourcesvc.LocalProvider) *Builder {
	b.localPrimitiveProvider = provider
	return b
}
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

// WithReportResources connects existing report tools to the shared namespaced
// catalog and operation-specific revision resolver.
func (b *Builder) WithReportResources(provider catalog.Provider, resolve reportingsvc.ResourceResolver) *Builder {
	b.reportCatalog = provider
	b.reportResourceResolver = resolve
	return b
}

func (b *Builder) WithReportResourceService(service primitive.ResourceAuthoring) *Builder {
	b.reportResourceService = service
	return b
}

func (b *Builder) WithReportingService(v *reportingsvc.Service) *Builder {
	b.reportingService = v
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
		LocalPrimitiveProvider: b.localPrimitiveProvider,
		Defaults:               b.defaults,
		ComponentDispatcher:    b.componentDispatcher,
		Native:                 b.native,
		MCPManager:             b.mcpManager,
		Store:                  b.store,
		KnowledgeStore:         b.knowledgeStore,
		StateStore:             b.stateStore,
		UIBridge:               b.uiBridge,
	}
	if out.UIBridge == nil {
		out.UIBridge = forgeuisvc.NewService(&forgeuisvc.Config{})
	}
	if out.Defaults == nil {
		out.Defaults = &config.Defaults{}
	}
	if err := out.Defaults.ToolExecutionProtection.Validate(); err != nil {
		return nil, err
	}

	var ownedNative *standalone.Server
	buildSucceeded := false
	defer func() {
		if !buildSucceeded {
			_ = out.Close(context.Background())
		}
	}()
	needsSQLReporting := b.reportingService == nil && out.Defaults.Reporting.Enabled && strings.EqualFold(strings.TrimSpace(resolveReportingStoreDefaults(out.Defaults).Backend), "sql")
	if out.Native == nil && (b.conversation == nil || b.data == nil || needsSQLReporting) {
		var err error
		if strings.TrimSpace(os.Getenv("AGENTLY_DB_DSN")) == "" && strings.TrimSpace(os.Getenv("AGENTLY_DB_PATH")) == "" {
			ownedNative, err = data.NewRuntimeFromWorkspace(ctx, workspace.RuntimeRoot())
		} else {
			ownedNative, err = data.NewRuntime(ctx)
		}
		if err != nil {
			return nil, fmt.Errorf("open linked Datly runtime: %w", err)
		}
		out.Native = ownedNative
		out.ownedNative = ownedNative
	}

	if out.AuthConfig == nil {
		authCfg, err := svcauth.LoadConfig(workspace.Root())
		if err != nil {
			return nil, err
		}
		out.AuthConfig = authCfg
	}
	if b.tokenProvider == nil {
		b.tokenProvider = svcauth.NewCreatedByUserTokenProvider(out.AuthConfig, out.Native)
	}
	// Delegated MCP OAuth (auth.mode=oauth with providerRef/inlineProvider):
	// build the workspace provider registry and credential resolver so the MCP
	// manager can install them for delegated configs only. Legacy MCP auth is
	// untouched when this stays nil.
	if b.mcpDelegatedAuth == nil {
		b.mcpDelegatedAuth = svcauth.NewDelegatedMCPAuth(out.AuthConfig, out.Native)
		if b.mcpDelegatedAuth != nil && out.Native != nil {
			// Gate delegated resolution and background refresh on the canonical
			// user's active status: disabled/deleted users fail closed.
			b.mcpDelegatedAuth.SetUserLookup(svcauth.NewDatlyUserService(out.Native))
		}
	}

	out.Conversation = b.conversation
	if out.Conversation == nil {
		cli, err := convsvc.New(ctx, out.Native)
		if err != nil {
			return nil, err
		}
		out.Conversation = cli
	}

	out.Data = b.data
	if out.Data == nil {
		out.Data = data.NewService(out.Native)
	}

	out.ElicitationRouter = b.elicRouter
	if out.ElicitationRouter == nil {
		out.ElicitationRouter = elicrouter.New()
	}

	out.Elicitation = elicsvc.New(out.Conversation, nil, out.ElicitationRouter, nil)

	out.MCPManager = b.mcpManager
	if out.MCPManager == nil {
		mgr, err := b.newDefaultMCPManager(out.Conversation, out.Elicitation, out.Defaults)
		if err != nil {
			return nil, err
		}
		out.MCPManager = mgr
		if ctx != nil {
			out.MCPManager.StartReaper(ctx, 5*time.Minute)
		}
	}

	out.Registry = b.registry
	if out.Registry == nil {
		reg, err := tool.NewDefaultRegistry(out.MCPManager)
		if err != nil {
			return nil, err
		}
		out.Registry = reg
	}
	// After a delegated MCP credential change (link/disconnect through the
	// hosted auth endpoints), evict the affected user's pooled MCP clients and
	// the registry discovery cooldown so the next call uses the new
	// credential. EffectiveUserID, sessions and the workspace provider are
	// untouched.
	if manager, registry := out.MCPManager, out.Registry; manager != nil {
		delegatedAuth := b.mcpDelegatedAuth
		svcauth.RegisterMCPAuthChangeListener(func(event svcauth.MCPAuthChangeEvent) {
			manager.EvictUserServer(event.EffectiveUserID, event.ServerName)
			if invalidator, ok := registry.(interface{ EvictDelegatedAuthState(server string) }); ok {
				invalidator.EvictDelegatedAuthState(event.ServerName)
			}
			delegatedAuth.ClearResolverCooldown(event.CanonicalUserID, event.StorageKey)
		})
	}
	if out.Defaults.ToolExecutionProtection.Enabled {
		guard, err := executionprotection.New(
			out.Defaults.ToolExecutionProtection,
			executionprotection.NewComponentRepository(out.Native),
		)
		if err != nil {
			return nil, err
		}
		if !tool.SetExecutionProtection(out.Registry, guard) {
			log.Printf("[warn] tool execution protection is enabled but the configured custom registry does not support the standard concrete registry guard")
		}
	}
	if !b.skipRegistryInitialize && !shouldSkipRegistryInitialize() {
		out.Registry.Initialize(ctx)
	}
	workspaceConfig, err := wscfg.Load(workspace.Root())
	if err != nil {
		return nil, err
	}
	if workspaceConfig != nil {
		uiSettings := workspaceConfig.UIAuthorizationSettings()
		policySettings := workspaceConfig.PolicyAuthorizationSettings()
		toolAuthorizerRef := ""
		if policySettings.Mode != "" && policySettings.Mode != "legacy-mcp" && policySettings.Mode != "authz" {
			return nil, fmt.Errorf("unsupported policy.authorization mode %q", policySettings.Mode)
		}
		switch uiSettings.Mode {
		case "", "legacy-mcp":
			if uiSettings.ProviderRef != "" || uiSettings.CapabilityMappingRef != "" {
				return nil, fmt.Errorf("ui.authorization legacy mode cannot configure authz references")
			}
			out.AuthorizationTool = uiSettings.LegacyTool
		case "authz":
			if uiSettings.LegacyTool != "" || uiSettings.ProviderRef == "" || uiSettings.CapabilityMappingRef == "" {
				return nil, fmt.Errorf("ui.authorization authz mode requires providerRef and capabilityMappingRef without tool")
			}
			provider, found := b.authorizationProviders[uiSettings.ProviderRef]
			mapping, mapped := b.capabilityMappings[uiSettings.CapabilityMappingRef]
			mappingV2, mappedV2 := b.capabilityMappingsV2[uiSettings.CapabilityMappingRef]
			if !found || (!mapped && !mappedV2) || provider.Service == nil || provider.Account == nil || provider.AuthorityRevision == nil || provider.AccountProjection == nil || provider.Gate == nil || provider.DatasourceAuthorize == nil || provider.PolicyVersion == "" || (mapping == nil && mappingV2 == nil) {
				return nil, fmt.Errorf("ui.authorization references are not registered")
			}
			out.PermittedResolver = &permittedview.AuthzResolver{Service: provider.Service, Version: provider.PolicyVersion, Account: provider.Account, AuthorityRevision: provider.AuthorityRevision, ProjectAccount: provider.AccountProjection, Map: mapping, MapV2: mappingV2, Gate: provider.Gate, EntityPermission: provider.EntityPermission, EntityPermissionWithLease: provider.EntityPermissionWithLease, EntityRoles: provider.EntityRoles}
			out.ComponentAuthority = provider.ComponentAuthoritySnapshot
			out.ExecutionContext = provider.ExecutionContext
			out.UIBridge.ConfigureWindowReadDecisionScope(provider.WindowReadDecisionScope)
			out.DatasourceAuthorizer = provider.DatasourceAuthorize
			out.DatasourceDefinitionAuthorizer = provider.DatasourceDefinitionAuthorize
			out.DatasourceDefinitionResolver = provider.DatasourceDefinitionResolver
			out.DatasourceResourceRevalidator = provider.DatasourceResourceRevalidator
			out.DatasourceDisableCache = true
			if provider.ToolAuthorize != nil {
				out.ToolAuthorizer, toolAuthorizerRef = provider.ToolAuthorize, uiSettings.ProviderRef
			}
		default:
			return nil, fmt.Errorf("unsupported ui.authorization mode %q", uiSettings.Mode)
		}
		if err := b.configurePrimitiveProviders(out); err != nil {
			return nil, err
		}
		var operations []string
		for section, operation := range map[string]string{
			"ui":            policy.OperationWindowView,
			"reports":       policy.OperationReportView,
			"starterPrompt": policy.OperationStarterPromptView,
			"intent":        policy.OperationIntentView,
		} {
			if workspaceConfig.PolicyAuthorizationEnabled(section) {
				operations = append(operations, operation)
			}
		}
		if len(operations) > 0 {
			if policySettings.Mode == "authz" && workspaceConfig.PolicyAuthorizationEnabled("ui") {
				ready, ok := any(out.UIBridge).(interface{ AuthzReady() bool })
				if !ok || !ready.AuthzReady() {
					return nil, fmt.Errorf("authz window admission requires a host-protected Forge catalog")
				}
			}
			switch policySettings.Mode {
			case "", "legacy-mcp":
				if policySettings.ProviderRef != "" {
					return nil, fmt.Errorf("policy.authorization legacy mode cannot configure providerRef")
				}
				if policySettings.LegacyTool == "" {
					return nil, fmt.Errorf("policy.authorization.mcpTool is required when an authorization section is enabled")
				}
				out.AuthorizationPolicy = policy.NewRuntime(&policy.MCPResolver{Executor: out.Registry, ToolName: policySettings.LegacyTool}, operations...)
			case "authz":
				if policySettings.LegacyTool != "" || policySettings.ProviderRef == "" {
					return nil, fmt.Errorf("policy.authorization authz mode requires providerRef without mcpTool")
				}
				provider, found := b.authorizationProviders[policySettings.ProviderRef]
				if !found || provider.Service == nil || provider.Account == nil || provider.Gate == nil || provider.PolicyResource == nil || provider.WindowAuthorize == nil || provider.PolicyVersion == "" {
					return nil, fmt.Errorf("policy.authorization providerRef is not registered")
				}
				out.WindowAuthorizer = provider.WindowAuthorize
				out.DatasourceDisableCache = true
				if workspaceConfig.PolicyAuthorizationEnabled("reports") {
					if provider.ReportAuthorize == nil {
						return nil, fmt.Errorf("authz reporting requires backend action authorizer")
					}
					out.ReportAuthorizer = provider.ReportAuthorize
				}
				if provider.ToolAuthorize != nil {
					if toolAuthorizerRef != "" && toolAuthorizerRef != policySettings.ProviderRef {
						return nil, fmt.Errorf("conflicting authz tool authorization providers")
					}
					out.ToolAuthorizer, toolAuthorizerRef = provider.ToolAuthorize, policySettings.ProviderRef
				}
				out.AuthorizationPolicy = policy.NewRuntime(&policy.AuthzResolver{Service: provider.Service, PolicyVersion: provider.PolicyVersion, Resource: provider.PolicyResource, Account: provider.Account, Gate: provider.Gate}, operations...)
				out.AuthorizationPolicy.ExactIDs = true
			default:
				return nil, fmt.Errorf("unsupported policy.authorization mode %q", policySettings.Mode)
			}
		} else if policySettings.Mode == "authz" {
			if policySettings.ProviderRef == "" || policySettings.LegacyTool != "" {
				return nil, fmt.Errorf("policy.authorization authz mode requires providerRef without mcpTool")
			}
			provider, found := b.authorizationProviders[policySettings.ProviderRef]
			if !found || provider.Service == nil || provider.Account == nil || provider.Gate == nil || provider.PolicyResource == nil || provider.WindowAuthorize == nil || provider.PolicyVersion == "" {
				return nil, fmt.Errorf("policy.authorization providerRef is not registered")
			}
			out.DatasourceDisableCache = true
		}
	}
	if out.DatasourceDisableCache && !tool.SetResultReuseDisabled(out.Registry, true) {
		return nil, fmt.Errorf("authz mode requires a tool registry without cross-account result reuse")
	}
	if out.ToolAuthorizer != nil && !tool.SetAuthorizationGuard(out.Registry, out.ToolAuthorizer) {
		return nil, fmt.Errorf("configured authz tool guard is unsupported by registry")
	}
	skillsvc.ExecFn = out.Registry.Execute
	out.Skills = skillsvc.New(out.Defaults, out.Conversation, b.agentFinder)
	out.Skills.SetToolRegistry(out.Registry)
	out.Skills.SetMCPSource(out.MCPManager)
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
		if out.Defaults != nil {
			resources := out.Defaults.Resources
			if strings.TrimSpace(resources.IndexPath) != "" {
				opts = append(opts, augmenter.WithIndexPathTemplate(resources.IndexPath))
			}
			if resources.UpstreamSyncConcurrency > 0 {
				opts = append(opts, augmenter.WithUpstreamSyncConcurrency(resources.UpstreamSyncConcurrency))
			}
			if resources.MatchConcurrency > 0 {
				opts = append(opts, augmenter.WithMatchConcurrency(resources.MatchConcurrency))
			}
			if resources.IndexAsync != nil {
				opts = append(opts, augmenter.WithIndexAsync(*resources.IndexAsync))
			}
		}
		if out.MCPManager != nil {
			opts = append(opts, augmenter.WithMCPManager(out.MCPManager))
		}
		aug = augmenter.New(b.embedderFinder, opts...)
		out.ownedAugmenter = aug
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

	out.GoalStore = b.goalStore
	if out.GoalStore == nil && out.Native != nil {
		out.GoalStore = goalsys.NewStore(out.Native)
	}
	out.Agent = b.agentSvc
	if out.Agent == nil {
		agentOpts := []agentsvc.Option{
			agentsvc.WithCancelRegistry(out.CancelRegistry),
			agentsvc.WithAuthorizationPolicy(out.AuthorizationPolicy),
			agentsvc.WithGoalStore(out.GoalStore),
		}
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
		promptRepo := intakerepo.NewWithStore(out.Store)
		templateRepo := tplrepo.NewWithStore(out.Store)
		bundleRepo := toolbundlerepo.NewWithStore(out.Store)
		intakeSvc := intakesvc.New(out.Core,
			intakesvc.WithConversationClient(out.Conversation),
			intakesvc.WithProfileRepo(promptRepo),
			intakesvc.WithTemplateRepo(templateRepo),
			intakesvc.WithBundleRepo(bundleRepo),
		)
		agentOpts = append(agentOpts, agentsvc.WithIntakeService(intakeSvc))
		agentOpts = append(agentOpts, agentsvc.WithSkillService(out.Skills))
		out.Agent = agentsvc.New(out.Core, b.agentFinder, aug, out.Registry, out.Defaults, out.Conversation, agentOpts...)
	}
	if out.Agent != nil {
		out.Agent.SetAuthorizationPolicy(out.AuthorizationPolicy)
		out.Agent.SetSkillService(out.Skills)
		out.Agent.SetUIBridge(out.UIBridge)
	}
	// Apply workspace-level async defaults (GC cadence, narrator timeout)
	// to the agent service's Manager. Parse errors surface loudly so
	// operator typos fail at bootstrap rather than silently degrading.
	if out.Agent != nil && out.Defaults != nil && out.Defaults.Async != nil {
		if _, _, _, err := out.Defaults.Async.Apply(ctx, out.Agent.AsyncManager()); err != nil {
			return nil, err
		}
	}
	if out.Skills != nil {
		if err := tool.AddInternalService(out.Registry, out.Skills); err != nil {
			return nil, err
		}
	}
	if (b.windowOpenBootstrap == nil) != (b.windowOpenSelection == nil) {
		return nil, fmt.Errorf("window open bootstrap requires explicit selection matcher")
	}
	if out.UIBridge != nil && out.UIBridge.HasWindowOpenAdmission() && b.windowOpenBootstrap != nil {
		return nil, fmt.Errorf("window open admission is already supplied by the host")
	}
	if out.UIBridge != nil && out.PermittedResolver != nil && !out.UIBridge.HasWindowOpenAdmission() {
		admission := &permittedview.OpenAdmission{Runtime: permittedview.NewRuntime(out.PermittedResolver), Bootstrap: b.windowOpenBootstrap, MatchSelection: b.windowOpenSelection}
		out.UIBridge.ConfigureWindowOpenAdmission(func(ctx context.Context, pin identity.ResolvedResource, window *forgetypes.Window, parameters map[string]any) (*forgeuisvc.WindowOpenDecision, error) {
			decision, err := admission.ApplyDecision(ctx, pin, window, parameters)
			if err != nil {
				return nil, err
			}
			return &forgeuisvc.WindowOpenDecision{Window: decision.Window, ValidUntil: decision.ExpiresAt}, nil
		})
	}
	if err := b.configureComponentTransport(out); err != nil {
		return nil, err
	}
	if out.Reporting == nil && b.reportingService != nil {
		out.Reporting = b.reportingService
		out.Reporting.SetAuthorizationPolicy(out.AuthorizationPolicy)
	}
	if out.Reporting != nil && out.ReportAuthorizer != nil {
		out.Reporting.SetActionAuthorizer(out.ReportAuthorizer)
	}
	scratchpadsvc.RegisterProvider()
	if out.Reporting == nil && out.Defaults != nil && out.Defaults.Reporting.Enabled {
		scratchpadTemplate := resolveScratchpadTemplate()
		scratchpadFS := afs.New()

		reportScratchpad := afsscratchpad.New(
			afsscratchpad.WithAFS(scratchpadFS),
			afsscratchpad.WithRootURI(scratchpadTemplate),
		)
		reportClient := reportfs.New(b.stateStore)
		reportStore := reportingsvc.NewStoreAdapter(reportClient)
		reportAudit := reportfs.NewAuditSink(b.stateStore)
		reportStoreDefaults := resolveReportingStoreDefaults(out.Defaults)
		if strings.EqualFold(strings.TrimSpace(reportStoreDefaults.Backend), "sql") {
			if out.Native == nil {
				return nil, fmt.Errorf("reporting sql store requires a native Datly runtime")
			}
			sqlClient, err := reportsql.New(ctx, out.Native, b.stateStore, reportfs.New(b.stateStore))
			if err != nil {
				return nil, err
			}
			reportClient = sqlClient
			reportStore = reportingsvc.NewStoreAdapter(sqlClient)
			if sqlStore, ok := sqlClient.(*reportsql.Store); ok {
				reportAudit = reportsql.NewAuditSink(sqlStore)
			}
		}
		out.ReportingClient = reportClient
		var activeRunResolver reportingsvc.ActiveReportRunResolver
		if out.Defaults.Reporting.BrowserRunPersistenceEnabled() {
			runClient, ok := reportClient.(reportstore.RunClient)
			if !ok {
				return nil, fmt.Errorf("reporting store does not implement browser report run persistence")
			}
			out.ReportRuns = reportingrunsvc.New(reportingrunsvc.Options{Store: runClient})
			if out.Defaults.Reporting.OrchestrationEnabled() {
				activeRunResolver = out.ReportRuns
			}
		}
		out.Reporting = reportingsvc.New(reportingsvc.Options{
			Compiler:                    reportingsvc.NewReportSpecCompiler(nil),
			Exporter:                    reportingsvc.NewForgeExporter(nil),
			Store:                       reportStore,
			ActiveRunResolver:           activeRunResolver,
			Audit:                       reportAudit,
			Scratchpad:                  reportScratchpad,
			ScratchpadFS:                scratchpadFS,
			TokenProvider:               b.tokenProvider,
			ExportFromRunEnabled:        out.Defaults.Reporting.ExportFromRunEnabled(),
			ConversationAdoptionEnabled: out.Defaults.Reporting.ConversationAdoptionEnabled(),
			AuthorizationPolicy:         out.AuthorizationPolicy,
			ActionAuthorize:             out.ReportAuthorizer,
		})
	}
	if b.reportCatalog != nil || b.reportResourceResolver != nil {
		if b.reportCatalog == nil || b.reportResourceResolver == nil || out.Reporting == nil {
			return nil, fmt.Errorf("report resource catalog, resolver and reporting service are required")
		}
		out.Reporting.SetReportResourceService(b.reportResourceService)
		out.Reporting.SetReportCatalog(b.reportCatalog)
		out.Reporting.SetResourceResolver(b.reportResourceResolver)
		if out.ReportRuns != nil {
			out.ReportRuns.RequireCanonicalExecution()
		}
		out.Reporting.SetResourceDatasetExecutor(reportingsvc.NewResourceDatasetExecutor(out.Registry, out.Reporting.AuthorizeResourceDataset, out.ComponentDispatcher))
	}
	if out.Registry != nil && out.Reporting != nil {
		if out.ReportAuthorizer != nil {
			out.Reporting.SetActionAuthorizer(out.ReportAuthorizer)
		}
		if err := tool.AddInternalService(out.Registry, out.Reporting); err != nil {
			return nil, err
		}
	}
	if out.Reporting != nil && out.Defaults != nil && out.Defaults.Reporting.Enabled {
		queueIntervalMs := out.Defaults.Reporting.QueueIntervalMs
		if queueIntervalMs <= 0 {
			queueIntervalMs = defaultReportingQueueIntervalMs
		}
		workerCtx, stopReporting := context.WithCancel(ctx)
		out.ownedReportingCancel = stopReporting
		if strings.EqualFold(strings.TrimSpace(resolveReportingStoreDefaults(out.Defaults).Backend), "sql") {
			workerCtx = reportsql.WithInternalAccess(workerCtx)
		}
		out.ReportingWorker = reportingsvc.NewWorker(out.Reporting, reportingsvc.WorkerOptions{
			Interval:   time.Duration(queueIntervalMs) * time.Millisecond,
			BatchLimit: out.Defaults.Reporting.QueueBatchLimit,
		})
		out.ownedReportingWorker = out.ReportingWorker
		if err := out.ReportingWorker.Start(workerCtx); err != nil {
			return nil, err
		}
	}
	out.Augmenter = aug
	if out.UIBridge != nil && out.UIBridge.UsesWindowResourceResolution() || out.Reporting != nil && out.Reporting.UsesResourceResolution() {
		out.RawResourceBoundary = workspace.NewRawResourceBoundary(out.Store.Root())
	}
	if resourcesSvc := resourcessvc.New(aug,
		resourcessvc.WithMCPManager(out.MCPManager),
		resourcessvc.WithConversationClient(out.Conversation),
		resourcessvc.WithAgentFinder(b.agentFinder),
		resourcessvc.WithDefaultEmbedder(out.Defaults.Embedder),
		resourcessvc.WithSkillService(out.Skills),
		resourcessvc.WithRawResourceBoundary(out.RawResourceBoundary),
	); resourcesSvc != nil {
		if err := tool.AddInternalService(out.Registry, resourcesSvc); err != nil {
			return nil, err
		}
	}
	promptRepo := intakerepo.NewWithStore(out.Store)
	if err := tool.AddInternalService(out.Registry, llmagents.New(out.Agent,
		llmagents.WithConversationClient(out.Conversation),
		llmagents.WithDataService(out.Data),
		llmagents.WithPromptRepo(promptRepo),
		llmagents.WithMCPManager(out.MCPManager),
		llmagents.WithModelFinder(b.modelFinder),
		llmagents.WithToolRegistry(out.Registry),
	)); err != nil {
		return nil, err
	}
	{
		promptSvc := promptsvc.New(promptRepo,
			promptsvc.WithConversationClient(out.Conversation),
			promptsvc.WithAgentFinder(b.agentFinder),
			promptsvc.WithMCPManager(out.MCPManager),
			promptsvc.WithAuthorizationPolicy(out.AuthorizationPolicy),
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

	if err := b.configurePrimitiveProviders(out); err != nil {
		return nil, err
	}
	if b.hotSwapEnabled {
		mgr, err := initHotSwap(ctx, b)
		if err != nil {
			return nil, err
		}
		out.HotSwap = mgr
	}
	buildSucceeded = true
	return out, nil
}

func shouldSkipRegistryInitialize() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("AGENTLY_SKIP_REGISTRY_INIT"))) {
	case "1", "true", "yes", "y", "on":
		return true
	}
	return false
}

func (b *Builder) newDefaultMCPManager(conv conversation.Client, elicitation *elicsvc.Service, defaults *config.Defaults) (*mcpmgr.Manager, error) {
	if conv == nil || elicitation == nil {
		return nil, fmt.Errorf("executor builder requires conversation and elicitation service for default MCP manager")
	}
	defaultModel := ""
	if defaults != nil {
		defaultModel = strings.TrimSpace(defaults.Model)
		if samplingModel := strings.TrimSpace(defaults.AgentAutoSelection.Model); samplingModel != "" {
			defaultModel = samplingModel
		}
	}
	opts := []mcpmgr.Option{
		mcpmgr.WithHandlerFactory(func() protoclient.Handler {
			handlerOptions := []mcpclienthandler.Option{}
			if b.modelFinder != nil {
				handlerOptions = append(handlerOptions, mcpclienthandler.WithModelFinder(b.modelFinder))
			}
			if defaultModel != "" {
				handlerOptions = append(handlerOptions, mcpclienthandler.WithDefaultModel(defaultModel))
			}
			return mcpclienthandler.New(elicitation, conv, handlerOptions...)
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
	if b.mcpDelegatedAuth != nil {
		opts = append(opts,
			mcpmgr.WithCredentialResolver(b.mcpDelegatedAuth.Resolver()),
			mcpmgr.WithProviderRegistry(b.mcpDelegatedAuth.Registry()),
		)
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

// Close releases resources created by this builder.
func (r *Runtime) Close(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if r == nil {
		return nil
	}
	r.closeOnce.Do(func() {
		if r.PrimitiveProviders != nil {
			r.PrimitiveProviders.Close()
		}
		r.registryWarmupMu.Lock()
		r.closed = true
		cancelWarmup, warmupDone := r.registryWarmupCancel, r.registryWarmupDone
		r.registryWarmupMu.Unlock()
		if cancelWarmup != nil {
			cancelWarmup()
		}
		if warmupDone != nil {
			select {
			case <-warmupDone:
			case <-ctx.Done():
				r.closeError = errors.Join(r.closeError, ctx.Err())
			}
		}
		r.registryWarmupMu.Lock()
		refreshDone := r.registryRefreshDone
		r.registryWarmupMu.Unlock()
		if refreshDone != nil {
			select {
			case <-refreshDone:
			case <-ctx.Done():
				r.closeError = errors.Join(r.closeError, ctx.Err())
			}
		}
		if r.ownedReportingCancel != nil {
			r.ownedReportingCancel()
		}
		if r.ownedReportingWorker != nil {
			r.closeError = errors.Join(r.closeError, r.ownedReportingWorker.Wait(ctx))
		}
		if r.ownedAugmenter != nil {
			r.closeError = errors.Join(r.closeError, r.ownedAugmenter.Close())
		}
		if r.ownedNative != nil {
			r.closeError = errors.Join(r.closeError, r.ownedNative.Shutdown(ctx))
		}
	})
	return r.closeError
}

// InitializeRegistryAsync owns one background warmup without closing a borrowed registry.
// Close cancels and joins this work before shutting down runtime resources.
func (r *Runtime) InitializeRegistryAsync(ctx context.Context, timeout time.Duration) <-chan struct{} {
	if ctx == nil {
		ctx = context.Background()
	}
	r.registryWarmupMu.Lock()
	defer r.registryWarmupMu.Unlock()
	if r.registryWarmupDone != nil {
		return r.registryWarmupDone
	}
	done := make(chan struct{})
	r.registryWarmupDone = done
	if r.closed || r.Registry == nil {
		close(done)
		return done
	}
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	lifetimeCtx, cancelLifetime := context.WithCancel(ctx)
	warmupCtx, cancelWarmup := context.WithTimeout(lifetimeCtx, timeout)
	r.registryWarmupCancel = func() { cancelWarmup(); cancelLifetime() }
	refreshDone := make(chan struct{})
	r.registryRefreshDone = refreshDone
	go func() {
		defer close(done)
		defer cancelWarmup()
		if initializer, ok := r.Registry.(interface {
			InitializeWithRefreshContext(context.Context, context.Context) <-chan struct{}
		}); ok {
			monitorsDone := initializer.InitializeWithRefreshContext(warmupCtx, lifetimeCtx)
			go func() {
				defer close(refreshDone)
				if monitorsDone != nil {
					<-monitorsDone
				}
			}()
		} else {
			defer close(refreshDone)
			defer cancelLifetime()
			r.Registry.Initialize(warmupCtx)
		}
	}()
	return done
}

// WithComponentDispatcher explicitly registers exact producer-supported
// component transport for both report runs and the public datasource stack.
func (b *Builder) WithComponentDispatcher(dispatcher windowprotocol.ComponentDispatcher) *Builder {
	b.componentDispatcher = dispatcher
	return b
}
