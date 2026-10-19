package tool

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	authctx "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/protocol/mcp/config"
	schema "github.com/viant/mcp-protocol/schema"
	client "github.com/viant/mcp/client"
)

type exactDispatchClient struct {
	discoveryListClient
	listCalls int
	callNames []string
}

func (c *exactDispatchClient) ListTools(context.Context, *string, ...client.RequestOption) (*schema.ListToolsResult, error) {
	c.listCalls++
	return nil, errors.New("tool discovery must not run during exact dispatch")
}

func (c *exactDispatchClient) CallTool(_ context.Context, params *schema.CallToolRequestParams, _ ...client.RequestOption) (*schema.CallToolResult, error) {
	c.callNames = append(c.callNames, params.Name)
	return &schema.CallToolResult{}, nil
}

func TestExactMCPDispatchDoesNotRediscoverPrivateCatalog(t *testing.T) {
	t.Setenv("AGENTLY_MCP_SERVERS", "external")
	remote := &exactDispatchClient{}
	manager := &discoveryManagerStub{
		options: &config.MCPClient{ToolsListVisibility: config.ToolsListVisibilityPrivate},
		getFunc: func(_, server string) (client.Interface, error) {
			require.Equal(t, "external", server)
			return remote, nil
		},
	}
	r := &Registry{
		mgr:   manager,
		cache: map[string]*toolCacheEntry{},
		discoveryShared: map[string]discoveryIdentity{
			"external|prior-conversation": {userID: "alice", catalogSeen: true},
		},
	}
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
	require.NoError(t, r.PreflightCredential(ctx, "external:literal.tool"))
	for i := 0; i < 2; i++ {
		_, err := r.ExecuteExactJSON(ctx, "external:literal.tool", map[string]interface{}{"term": "example"}, "")
		require.NoError(t, err)
	}
	require.Equal(t, []string{"literal.tool", "literal.tool"}, remote.callNames)
	require.Zero(t, remote.listCalls)
}

func TestExactNamespaceMCPDispatchPreservesResourceIdentity(t *testing.T) {
	const server = "mcp://catalog/nested/connection"
	t.Setenv("AGENTLY_MCP_SERVERS", server)
	remote := &exactDispatchClient{}
	manager := &discoveryManagerStub{
		options: &config.MCPClient{ToolsListVisibility: config.ToolsListVisibilityPrivate},
		getFunc: func(_, actual string) (client.Interface, error) {
			require.Equal(t, server, actual)
			return remote, nil
		},
	}
	r := &Registry{mgr: manager, cache: map[string]*toolCacheEntry{}, discoveryShared: map[string]discoveryIdentity{
		server + "|prior-conversation": {userID: "alice", catalogSeen: true},
	}}
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
	for _, method := range []string{"literal.tool", "nested/tool:operation"} {
		name := server + ":" + method
		require.NoError(t, r.PreflightCredential(ctx, name))
		_, err := r.ExecuteExactJSON(ctx, name, map[string]interface{}{"unchanged": true}, "")
		require.NoError(t, err)
	}
	require.Equal(t, []string{"literal.tool", "nested/tool:operation"}, remote.callNames)
	require.Zero(t, remote.listCalls, "exact namespace routing must reuse the existing catalog")
}
