package server

import (
	"context"
	"log"
	"net/http"
	"strings"
	"sync"

	"github.com/viant/afs"
	agentfinder "github.com/viant/agently-core/protocol/agent/finder"
	agentloader "github.com/viant/agently-core/protocol/agent/loader"
	integrate "github.com/viant/agently-core/protocol/mcp/auth/integrate"
	mcpcookies "github.com/viant/agently-core/protocol/mcp/cookies"
	mcprepo "github.com/viant/agently-core/workspace/repository/mcp"
	"github.com/viant/mcp/client/auth/transport"

	"github.com/viant/agently-core/app/executor"
	execconfig "github.com/viant/agently-core/app/executor/config"
	cancels "github.com/viant/agently-core/app/store/conversation/cancel"
	embedprovider "github.com/viant/agently-core/genai/embedder/provider"
	provider "github.com/viant/agently-core/genai/llm/provider"
	embedderfinder "github.com/viant/agently-core/internal/finder/embedder"
	modelfinder "github.com/viant/agently-core/internal/finder/model"
	"github.com/viant/agently-core/internal/sdkbackend"
	agentmodel "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/primitive/observe"
	"github.com/viant/agently-core/sdk"
	svcauth "github.com/viant/agently-core/service/auth"
	"github.com/viant/agently-core/workspace"
	wsconfig "github.com/viant/agently-core/workspace/config"
	embedderloader "github.com/viant/agently-core/workspace/loader/embedder"
	wsfs "github.com/viant/agently-core/workspace/loader/fs"
	modelloader "github.com/viant/agently-core/workspace/loader/model"
	"github.com/viant/agently-core/workspace/service/meta"
)

type RuntimeOptions struct {
	WorkspaceRoot          string
	Defaults               *execconfig.Defaults
	SchedulerHeadless      bool
	SkipRegistryInitialize bool
	RecordOOBAuthURL       func(context.Context, string) error
	ConfigureRuntime       func(context.Context, *executor.Runtime, string)
	// ConfigureBuilder registers trusted host authorization providers and
	// mappings before workspace authorization mode is constructed.
	ConfigureBuilder func(context.Context, *executor.Builder) error
}

type oobAuthRecorder interface {
	RecordOOBAuthElicitation(context.Context, string) error
}

func BuildWorkspaceRuntime(ctx context.Context, opts RuntimeOptions) (*executor.Runtime, sdk.Backend, agentmodel.Finder, error) {
	fs := afs.New()
	workspaceRoot := strings.TrimSpace(opts.WorkspaceRoot)
	if workspaceRoot != "" {
		workspace.SetRoot(workspaceRoot)
	}
	defaults := opts.Defaults
	var workspaceConfig *wsconfig.Root
	workspaceTiming := observe.Start(ctx, "startup.runtime.workspace_config", observe.Labels{})
	if cfg, err := wsconfig.Load(workspace.Root()); err != nil {
		workspaceTiming.End(observe.OutcomeFor(err), observe.CacheBypass)
		return nil, nil, nil, err
	} else {
		workspaceConfig = cfg
		defaults, err = cfg.ResolveDefaultsWithFallback(defaults)
		if err != nil {
			workspaceTiming.End(observe.OutcomeFor(err), observe.CacheBypass)
			return nil, nil, nil, err
		}
	}
	wsconfig.ApplyPathDefaults(defaults)
	workspaceTiming.End(observe.OK, observe.CacheBypass)
	wsMeta := meta.New(fs, workspaceRoot)
	agentLdr := agentloader.New(agentloader.WithMetaService(wsMeta))
	agentFndr := agentfinder.New(agentfinder.WithLoader(agentLdr))
	modelLdr := modelloader.New(wsfs.WithMetaService[provider.Config](wsMeta))
	modelFndr := modelfinder.New(modelfinder.WithConfigLoader(modelLdr))
	embedderLdr := embedderloader.New(wsfs.WithMetaService[embedprovider.Config](wsMeta))
	embedderFndr := embedderfinder.New(embedderfinder.WithConfigLoader(embedderLdr))
	cancelRegistry := cancels.NewMemory()

	mcpRepo := mcprepo.New(fs)
	cookieProvider := mcpcookies.New(fs, mcpRepo)
	jarProvider := cookieProvider.Jar
	var recorder oobAuthRecorder
	var (
		rtMu     sync.Mutex
		rtByUser = map[string]*transport.RoundTripper{}
	)
	authRTProvider := func(ctx context.Context) *transport.RoundTripper {
		user := strings.TrimSpace(svcauth.EffectiveUserID(ctx))
		if user == "" {
			user = "anonymous"
		}
		rtMu.Lock()
		defer rtMu.Unlock()
		if v, ok := rtByUser[user]; ok && v != nil {
			return v
		}
		j, jerr := jarProvider(ctx)
		if jerr != nil {
			return nil
		}
		var authRT *transport.RoundTripper
		if opts.SchedulerHeadless {
			authRT, _ = integrate.NewHeadlessAuthRoundTripper(j, http.DefaultTransport, 0)
		} else {
			authRT, _ = integrate.NewAuthRoundTripperWithElicitation(j, http.DefaultTransport, 0, func(ctx context.Context, authURL string) error {
				if opts.RecordOOBAuthURL != nil {
					return opts.RecordOOBAuthURL(ctx, authURL)
				}
				if recorder != nil {
					return recorder.RecordOOBAuthElicitation(ctx, authURL)
				}
				log.Printf("[mcp-auth] OAuth URL (client not ready): %s", authURL)
				return nil
			})
		}
		rtByUser[user] = authRT
		return authRT
	}

	builder := executor.NewBuilder().
		WithAgentFinder(agentFndr).
		WithModelFinder(modelFndr).
		WithEmbedderFinder(embedderFndr).
		WithCancelRegistry(cancelRegistry).
		WithDefaults(defaults).
		WithSkipRegistryInitialize(opts.SkipRegistryInitialize).
		WithMCPAuthRTProvider(authRTProvider).
		WithMCPCookieJarProvider(jarProvider).
		WithMCPUserIDExtractor(func(ctx context.Context) string {
			return strings.TrimSpace(svcauth.EffectiveUserID(ctx))
		})
	if opts.ConfigureBuilder != nil {
		configureTiming := observe.Start(ctx, "startup.runtime.configure_builder", observe.Labels{})
		if err := opts.ConfigureBuilder(ctx, builder); err != nil {
			configureTiming.End(observe.OutcomeFor(err), observe.CacheBypass)
			return nil, nil, nil, err
		}
		configureTiming.End(observe.OK, observe.CacheBypass)
	}
	buildTiming := observe.Start(ctx, "startup.runtime.executor_build", observe.Labels{})
	rt, err := builder.Build(ctx)
	buildTiming.End(observe.OutcomeFor(err), observe.CacheBypass)
	if err != nil {
		return nil, nil, nil, err
	}
	if workspaceConfig != nil {
		rt.AuthorizationTool = workspaceConfig.AuthorizationTool()
	}
	if opts.ConfigureRuntime != nil {
		runtimeTiming := observe.Start(ctx, "startup.runtime.configure_runtime", observe.Labels{})
		opts.ConfigureRuntime(ctx, rt, workspaceRoot)
		runtimeTiming.End(observe.OK, observe.CacheBypass)
	}
	backendTiming := observe.Start(ctx, "startup.runtime.sdk_backend", observe.Labels{})
	client, err := sdkbackend.FromRuntime(rt)
	backendTiming.End(observe.OutcomeFor(err), observe.CacheBypass)
	if err != nil {
		_ = rt.Close(context.Background())
		return nil, nil, nil, err
	}
	if value, ok := client.(oobAuthRecorder); ok {
		recorder = value
	}
	return rt, client, agentFndr, nil
}
