package sdk

import (
	"context"
	"github.com/stretchr/testify/require"
	authctx "github.com/viant/agently-core/internal/auth"
	convservice "github.com/viant/agently-core/internal/service/conversation"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	mcpmgr "github.com/viant/agently-core/protocol/mcp/manager"
	agentsvc "github.com/viant/agently-core/service/agent"
	"github.com/viant/mcp"
	"testing"
)

type nativeAppOptions struct{ endpoint string }

func (p *nativeAppOptions) Options(context.Context, string) (*mcpcfg.MCPClient, error) {
	return &mcpcfg.MCPClient{ClientOptions: &mcp.ClientOptions{Transport: mcp.ClientTransport{Type: "http", ClientTransportHTTP: mcp.ClientTransportHTTP{URL: p.endpoint}}}}, nil
}

func TestMCPAppsNativeHostRechecksPrincipalBindingAndServer(t *testing.T) {
	enableAtomicGoals(t)
	runtime, _, db := atomicGoalFixture(t)
	_, err := db.Exec(`UPDATE conversation SET created_by_user_id='owner',visibility='private',agent_id='configured' WHERE id='goal-thread'`)
	require.NoError(t, err)
	owner := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "owner"})
	foreign := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "foreign"})
	conv, err := convservice.New(owner, runtime)
	require.NoError(t, err)
	provider := &nativeAppOptions{endpoint: "http://127.0.0.1:18244/mcp"}
	manager, err := mcpmgr.New(provider)
	require.NoError(t, err)
	finder := &streamTestAgentFinder{agent: &agentmdl.Agent{}}
	backend := &backendClient{goalInvoker: runtime, conv: conv, mcpMgr: manager, agent: agentsvc.New(nil, finder, nil, &integrationToolRegistry{}, nil, conv)}
	host := backend.aguiMCPAppsHost()
	require.NotNil(t, host)
	hash, err := MCPAppsServerHash("http", provider.endpoint)
	require.NoError(t, err)
	app := AGUIMCPAppBinding{AppInstanceID: "trusted", ThreadID: "goal-thread", ServerID: "configured", ServerHash: hash, ResourceURI: "ui://configured/app"}
	binding := host.Bind(owner, app)
	require.NoError(t, binding.Authorize(owner, app))
	require.Error(t, binding.Authorize(foreign, app))
	changed := app
	changed.ThreadID = "foreign-native-id"
	require.Error(t, binding.Authorize(owner, changed))
	_, err = binding.ResourceReader(owner, app.ServerID, "ui://other/app")
	require.Error(t, err)
	_, err = binding.ToolCaller(owner, &MCPUIToolCallInput{ConversationID: "caller-native-id", ToolName: "configured-read", ToolBundles: []string{"caller-grant"}})
	require.Error(t, err)
	require.Error(t, binding.AuthorizeTool(owner, app, "read"))
	provider.endpoint = "http://127.0.0.1:18245/mcp"
	require.Error(t, binding.Authorize(owner, app))
	require.Nil(t, binding.Notification)
}

func TestMCPAppsNativeHostUnavailableWithoutNativeRuntime(t *testing.T) {
	require.Nil(t, (&backendClient{}).aguiMCPAppsHost())
}
