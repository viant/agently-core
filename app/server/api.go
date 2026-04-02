package server

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/viant/agently-core/app/executor"
	"github.com/viant/agently-core/genai/llm"
	agentmodel "github.com/viant/agently-core/protocol/agent"
	mcpexpose "github.com/viant/agently-core/protocol/mcp/expose"
	"github.com/viant/agently-core/protocol/tool"
	"github.com/viant/agently-core/sdk"
	svca2a "github.com/viant/agently-core/service/a2a"
	svcauth "github.com/viant/agently-core/service/auth"
	svcscheduler "github.com/viant/agently-core/service/scheduler"
	svcworkspace "github.com/viant/agently-core/service/workspace"
)

type APIOptions struct {
	Version          string
	Runtime          *executor.Runtime
	Client           sdk.Client
	AgentFinder      agentmodel.Finder
	AgentIDs         []string
	AuthRuntime      *svcauth.Runtime
	SchedulerService *svcscheduler.Service
	SchedulerOptions *sdk.SchedulerOptions
}

func NewAPIHandler(ctx context.Context, opts APIOptions) (http.Handler, error) {
	metadataVersion := strings.TrimSpace(opts.Version)
	if metadataVersion == "" {
		metadataVersion = "agently-core"
	}
	metadataHandler := svcworkspace.NewMetadataHandler(opts.Runtime.Defaults, opts.Runtime.Store, metadataVersion)
	fileBrowserHandler := svcworkspace.NewFileBrowserHandler()
	a2aSvc := svca2a.New(opts.Runtime.Agent, opts.AgentFinder)
	a2aHandler := svca2a.NewHandler(a2aSvc)

	handlerOpts := []sdk.HandlerOption{
		sdk.WithMetadataHandler(metadataHandler),
		sdk.WithFileBrowser(fileBrowserHandler),
		sdk.WithA2AHandler(a2aHandler),
	}
	if opts.SchedulerService != nil && opts.SchedulerOptions != nil && (opts.SchedulerOptions.EnableAPI || opts.SchedulerOptions.EnableWatchdog) {
		handlerOpts = append(handlerOpts, sdk.WithScheduler(opts.SchedulerService, svcscheduler.NewHandler(opts.SchedulerService), opts.SchedulerOptions))
	}
	sdkHandler, err := sdk.NewHandlerWithContext(ctx, opts.Client, handlerOpts...)
	if err != nil {
		return nil, err
	}
	svca2a.StartServers(ctx, &svca2a.ServerConfig{
		AgentService:  opts.Runtime.Agent,
		AgentFinder:   opts.AgentFinder,
		AgentIDs:      append([]string(nil), opts.AgentIDs...),
		JWTService:    opts.AuthRuntime.JWTService(),
		TokenProvider: opts.Runtime.TokenProvider,
	})
	return svcauth.WithAuthExtensions(sdkHandler, opts.AuthRuntime), nil
}

func NewExposedMCPServer(ctx context.Context, rt *executor.Runtime, cfg *mcpexpose.ServerConfig, authRuntime *svcauth.Runtime) (*http.Server, error) {
	server, err := mcpexpose.NewHTTPServer(ctx, &runtimeExecutorAdapter{rt: rt}, cfg)
	if err != nil {
		return nil, err
	}
	server.Handler = svcauth.WithAuthProtection(server.Handler, authRuntime)
	return server, nil
}

func DiscoverWorkspaceAgentIDs(workspaceRoot string) []string {
	root := filepath.Join(strings.TrimSpace(workspaceRoot), "agents")
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	seen := map[string]struct{}{}
	var ids []string
	for _, entry := range entries {
		name := strings.TrimSpace(entry.Name())
		if name == "" {
			continue
		}
		if entry.IsDir() {
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			ids = append(ids, name)
			continue
		}
		if filepath.Ext(name) != ".yaml" {
			continue
		}
		id := strings.TrimSuffix(name, filepath.Ext(name))
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids
}

type runtimeExecutorAdapter struct {
	rt *executor.Runtime
}

type registryLLMCore struct {
	reg tool.Registry
}

func (a *runtimeExecutorAdapter) LLMCore() mcpexpose.LLMCore {
	return &registryLLMCore{reg: a.rt.Registry}
}

func (a *runtimeExecutorAdapter) ExecuteTool(ctx context.Context, name string, args map[string]interface{}, _ int) (interface{}, error) {
	return a.rt.Registry.Execute(ctx, name, args)
}

func (r *registryLLMCore) ToolDefinitions() []llm.ToolDefinition {
	return r.reg.Definitions()
}
