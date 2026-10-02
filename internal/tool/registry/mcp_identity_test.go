package tool

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	authctx "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcpname"
	"github.com/viant/agently-core/runtime/requestctx"
	schema "github.com/viant/mcp-protocol/schema"
	client "github.com/viant/mcp/client"
)

type exactIdentityClient struct {
	discoveryListClient
	called string
}

func (c *exactIdentityClient) CallTool(_ context.Context, p *schema.CallToolRequestParams, _ ...client.RequestOption) (*schema.CallToolResult, error) {
	c.called = p.Name
	return &schema.CallToolResult{}, nil
}

func TestAdvertisedMCPIdentityUsesLiteralServerAndTool(t *testing.T) {
	for _, tc := range []struct{ server, tool string }{{"datly", "datly.components"}, {"server_name", "tool_with_underscores"}, {"server-name", "tool-with-hyphens"}, {"server.name", "tool.name-with_parts"}, {"server/path", "tool_with-parts.name"}} {
		t.Run(tc.server+"/"+tc.tool, func(t *testing.T) {
			remote := &exactIdentityClient{}
			manager := &discoveryManagerStub{options: &config.MCPClient{ToolsListVisibility: config.ToolsListVisibilityPublic}, getFunc: func(_ string, server string) (client.Interface, error) {
				if server != tc.server {
					return nil, fmt.Errorf("wrong server %q", server)
				}
				return remote, nil
			}}
			r := &Registry{mgr: manager, cache: map[string]*toolCacheEntry{}}
			r.mergeServerTools(tc.server, []schema.Tool{{Name: tc.tool}})
			alias := mcpname.Canonical(tc.server + "/" + tc.tool)
			_, err := r.Execute(context.Background(), alias, map[string]interface{}{"owned": true})
			require.NoError(t, err)
			require.Equal(t, tc.tool, remote.called)
			def, ok := r.GetDefinitionWithContext(context.Background(), alias)
			require.True(t, ok)
			require.NotNil(t, def)
		})
	}
}

func TestAdvertisedMCPIdentityRejectsNormalizedCollision(t *testing.T) {
	manager := &discoveryManagerStub{options: &config.MCPClient{ToolsListVisibility: config.ToolsListVisibilityPublic}, getFunc: func(_, server string) (client.Interface, error) {
		t.Fatalf("ambiguous alias dispatched to %s", server)
		return nil, nil
	}}
	r := &Registry{mgr: manager, cache: map[string]*toolCacheEntry{}}
	r.mergeServerTools("a_b", []schema.Tool{{Name: "tool.name"}})
	r.mergeServerTools("a/b", []schema.Tool{{Name: "tool.name"}})
	_, err := r.Execute(context.Background(), mcpname.Canonical("a_b/tool.name"), nil)
	require.ErrorContains(t, err, "ambiguous advertised MCP tool alias")
}

func TestAdvertisedMCPIdentityKeepsPrincipalCatalogScoped(t *testing.T) {
	manager := &discoveryManagerStub{options: &config.MCPClient{ToolsListVisibility: config.ToolsListVisibilityPrivate}}
	r := &Registry{mgr: manager, cache: map[string]*toolCacheEntry{}}
	r.storePrincipalDiscoveryTools("private_server", "alice", "", false, []schema.Tool{{Name: "literal.tool-name"}})
	alice := requestctx.WithConversationID(authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"}), "owned")
	bob := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "bob"})
	alias := mcpname.Canonical("private_server/literal.tool-name")
	server, method, ok, err := r.discoveredMCPIdentity(alice, alias)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "private_server", server)
	require.Equal(t, "literal.tool-name", method)
	_, _, ok, err = r.discoveredMCPIdentity(bob, alias)
	require.NoError(t, err)
	require.False(t, ok)
	_, _, ok, err = r.discoveredMCPIdentity(context.Background(), alias)
	require.NoError(t, err)
	require.False(t, ok)
	r.storePrincipalDiscoveryTools("private_server", "alice", "different-token", false, []schema.Tool{{Name: "not.visible"}})
	_, _, ok, err = r.discoveredMCPIdentity(alice, mcpname.Canonical("private_server/not.visible"))
	require.NoError(t, err)
	require.False(t, ok)
	require.Empty(t, r.cache)
}

func TestAdvertisedIdentityDoesNotReuseCatalogAfterVisibilityBecomesPrivate(t *testing.T) {
	manager := &discoveryManagerStub{options: &config.MCPClient{ToolsListVisibility: config.ToolsListVisibilityPublic}}
	r := &Registry{mgr: manager, cache: map[string]*toolCacheEntry{}}
	r.mergeServerTools("server_name", []schema.Tool{{Name: "literal.tool"}})
	manager.options = &config.MCPClient{ToolsListVisibility: config.ToolsListVisibilityPrivate}
	_, _, ok, err := r.discoveredMCPIdentity(context.Background(), mcpname.Canonical("server_name/literal.tool"))
	require.NoError(t, err)
	require.False(t, ok)
	_, ok = r.GetDefinitionWithContext(context.Background(), "server_name/literal.tool")
	require.False(t, ok)
	_, ok = r.lookupTimeoutSupport("server_name/literal.tool")
	require.False(t, ok)
}
