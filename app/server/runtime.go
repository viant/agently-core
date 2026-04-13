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
	"github.com/viant/agently-core/sdk"
	svcauth "github.com/viant/agently-core/service/auth"
	embedderloader "github.com/viant/agently-core/workspace/loader/embedder"
	wsfs "github.com/viant/agently-core/workspace/loader/fs"
	modelloader "github.com/viant/agently-core/workspace/loader/model"
	"github.com/viant/agently-core/workspace/service/meta"
)

type RuntimeOptions struct {
	WorkspaceRoot     string
	Defaults          *execconfig.Defaults
	SchedulerHeadless bool
	RecordOOBAuthURL  func(context.Context, string) error
	ConfigureRuntime  func(context.Context, *executor.Runtime, string)
}

type oobAuthRecorder interface {
	RecordOOBAuthElicitation(context.Context, string) error
}

func BuildWorkspaceRuntime(ctx context.Context, opts RuntimeOptions) (*executor.Runtime, sdk.Backend, agentmodel.Finder, error) {
	fs := afs.New()
	workspaceRoot := strings.TrimSpace(opts.WorkspaceRoot)
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

	rt, err := executor.NewBuilder().
		WithAgentFinder(agentFndr).
		WithModelFinder(modelFndr).
		WithEmbedderFinder(embedderFndr).
		WithCancelRegistry(cancelRegistry).
		WithDefaults(opts.Defaults).
		WithMCPAuthRTProvider(authRTProvider).
		WithMCPCookieJarProvider(jarProvider).
		WithMCPUserIDExtractor(func(ctx context.Context) string {
			return strings.TrimSpace(svcauth.EffectiveUserID(ctx))
		}).
		Build(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	if opts.ConfigureRuntime != nil {
		opts.ConfigureRuntime(ctx, rt, workspaceRoot)
	}
	client, err := sdkbackend.FromRuntime(rt)
	if err != nil {
		return nil, nil, nil, err
	}
	if value, ok := client.(oobAuthRecorder); ok {
		recorder = value
	}
	return rt, client, agentFndr, nil
}
