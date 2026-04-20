package tool_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	registry "github.com/viant/agently-core/internal/tool/registry"
	asynccfg "github.com/viant/agently-core/protocol/async"
	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcp/manager"
	agents "github.com/viant/agently-core/protocol/tool/service/llm/agents"
	execsvc "github.com/viant/agently-core/protocol/tool/service/system/exec"
)

func TestRegistry_AsyncConfig_InternalServices(t *testing.T) {
	mgr, err := manager.New(nil)
	require.NoError(t, err)
	reg, err := registry.NewWithManager(mgr)
	require.NoError(t, err)
	require.NoError(t, reg.AddInternalService(agents.New(nil)))
	require.NoError(t, reg.AddInternalService(execsvc.New()))

	agentCfg, ok := reg.AsyncConfig("llm/agents:start")
	require.True(t, ok)
	require.NotNil(t, agentCfg)
	require.Equal(t, "conversationId", agentCfg.Run.OperationIDPath)

	execCfg, ok := reg.AsyncConfig("system/exec:start")
	require.True(t, ok)
	require.NotNil(t, execCfg)
	require.Equal(t, "sessionId", execCfg.Run.OperationIDPath)

	aliasCfg, ok := reg.AsyncConfig("system/exec/start")
	require.True(t, ok)
	require.Same(t, execCfg, aliasCfg)

	cancelAlias, ok := reg.AsyncConfig("llm/agents/cancel")
	require.True(t, ok)
	require.Same(t, agentCfg, cancelAlias)

	_, ok = reg.AsyncConfig("llm/agents:list")
	require.False(t, ok)
}

type asyncProvider struct {
	cfg *mcpcfg.MCPClient
}

func (a *asyncProvider) Options(context.Context, string) (*mcpcfg.MCPClient, error) {
	return a.cfg, nil
}

func TestRegistry_AsyncConfig_MCPOptionsFallback(t *testing.T) {
	mgr, err := manager.New(&asyncProvider{cfg: &mcpcfg.MCPClient{
		Async: []*asynccfg.Config{{
			DefaultExecutionMode: string(asynccfg.ExecutionModeWait),
			Run: asynccfg.RunConfig{
				Tool:            "forecasting:start",
				OperationIDPath: "taskId",
			},
			Status: asynccfg.StatusConfig{
				Tool:           "forecasting:status",
				OperationIDArg: "taskId",
				Selector:       asynccfg.Selector{StatusPath: "status"},
			},
		}},
	}})
	require.NoError(t, err)

	reg, err := registry.NewWithManager(mgr)
	require.NoError(t, err)

	cfg, ok := reg.AsyncConfig("forecasting:status")
	require.True(t, ok)
	require.NotNil(t, cfg)
	require.Equal(t, "taskId", cfg.Status.OperationIDArg)

	aliasCfg, ok := reg.AsyncConfig("forecasting/status")
	require.True(t, ok)
	require.Same(t, cfg, aliasCfg)
}
