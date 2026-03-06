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
	iauth "github.com/viant/agently-core/internal/auth"
	token "github.com/viant/agently-core/internal/auth/token"
	convsvc "github.com/viant/agently-core/internal/service/conversation"
	agentmodel "github.com/viant/agently-core/protocol/agent"
	mcpclienthandler "github.com/viant/agently-core/protocol/mcp/clienthandler"
	mcpmgr "github.com/viant/agently-core/protocol/mcp/manager"
	"github.com/viant/agently-core/protocol/tool"
	"github.com/viant/agently-core/runtime/streaming"
	agentsvc "github.com/viant/agently-core/service/agent"
	"github.com/viant/agently-core/service/augmenter"
	"github.com/viant/agently-core/service/core"
	modelcallctx "github.com/viant/agently-core/service/core/modelcall"
	elicsvc "github.com/viant/agently-core/service/elicitation"
	elicrouter "github.com/viant/agently-core/service/elicitation/router"
	"github.com/viant/agently-core/workspace"
	"github.com/viant/agently-core/workspace/hotswap"
	embedderloader "github.com/viant/agently-core/workspace/loader/embedder"
	modelloader "github.com/viant/agently-core/workspace/loader/model"
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
	Streaming         streaming.Bus
	HotSwap           *hotswap.Manager
	Store             workspace.Store
	KnowledgeStore    workspace.KnowledgeStore
	StateStore        workspace.StateStore

	// AuthConfig holds the auth configuration when auth is enabled.
	AuthConfig *iauth.Config
	// AuthMiddleware is the HTTP middleware that extracts auth from requests.
	AuthMiddleware func(http.Handler) http.Handler

	// TokenProvider manages auth token lifecycle (cache, refresh, persistence).
	TokenProvider token.Provider
}

type Builder struct {
	defaults       *config.Defaults
	dao            *datly.Service
	conversation   conversation.Client
	data           data.Service
	registry       tool.Registry
	core           *core.Service
	agentSvc       *agentsvc.Service
	agentFinder    agentmodel.Finder
	agentLoader    agentmodel.Loader
	modelFinder    llm.Finder
	modelLoader    *modelloader.Service
	embedderFinder embedder.Finder
	embedderLoader *embedderloader.Service
	augmenter      *augmenter.Service
	mcpManager     *mcpmgr.Manager
	cancelRegistry cancels.Registry
	elicRouter     elicrouter.ElicitationRouter
	streamPub      modelcallctx.StreamPublisher
	streamBus      streaming.Bus
	hotSwapEnabled bool
	store          workspace.Store
	knowledgeStore workspace.KnowledgeStore
	stateStore     workspace.StateStore
	tokenProvider  token.Provider
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
func (b *Builder) WithCancelRegistry(v cancels.Registry) *Builder  { b.cancelRegistry = v; return b }
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
			dao, err = data.NewDatlyInMemory(ctx)
		} else {
			dao, err = data.NewDatly(ctx)
		}
		if err != nil {
			return nil, err
		}
		out.DAO = dao
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

	out.MCPManager = b.mcpManager
	if out.MCPManager == nil {
		mgr, err := b.newDefaultMCPManager(out.Conversation, out.ElicitationRouter)
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
	out.Streaming = b.streamBus
	if out.Streaming == nil {
		out.Streaming = streaming.NewMemoryBus(0)
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

	out.Agent = b.agentSvc
	if out.Agent == nil {
		agentOpts := []agentsvc.Option{}
		if b.cancelRegistry != nil {
			agentOpts = append(agentOpts, agentsvc.WithCancelRegistry(b.cancelRegistry))
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
		out.Agent = agentsvc.New(out.Core, b.agentFinder, aug, out.Registry, out.Defaults, out.Conversation, agentOpts...)
	}

	out.TokenProvider = b.tokenProvider

	if b.hotSwapEnabled {
		mgr, err := initHotSwap(ctx, b)
		if err != nil {
			return nil, err
		}
		out.HotSwap = mgr
	}
	return out, nil
}

func (b *Builder) newDefaultMCPManager(conv conversation.Client, router elicrouter.ElicitationRouter) (*mcpmgr.Manager, error) {
	if conv == nil || router == nil {
		return nil, fmt.Errorf("executor builder requires conversation and elicitation router for default MCP manager")
	}
	elicitation := elicsvc.New(conv, nil, router, nil)
	return mcpmgr.New(
		mcpmgr.NewRepoProvider(),
		mcpmgr.WithHandlerFactory(func() protoclient.Handler {
			return mcpclienthandler.New(elicitation, conv)
		}),
	)
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
