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
