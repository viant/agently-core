package tool

import (
	"context"
	"fmt"
	authctx "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/runtime/requestctx"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcpname"
	schema "github.com/viant/mcp-protocol/schema"
	client "github.com/viant/mcp/client"
)

type exactIdentityClient struct {
	discoveryListClient
	called string
}

type countedIdentityManager struct {
	discoveryManagerStub
	optionCalls int
}

func (m *countedIdentityManager) Options(ctx context.Context, server string) (*config.MCPClient, error) {
	m.optionCalls++
	return m.discoveryManagerStub.Options(ctx, server)
}

func TestAdvertisedIdentityChecksOnlyMatchingCatalogAuthority(t *testing.T) {
	m := &countedIdentityManager{discoveryManagerStub: discoveryManagerStub{options: &config.MCPClient{ToolsListVisibility: config.ToolsListVisibilityPublic}}}
	r := &Registry{mgr: m, cache: map[string]*toolCacheEntry{}}
	tools := make([]schema.Tool, 2000)
	for i := range tools {
		tools[i].Name = fmt.Sprintf("tool%d", i)
	}
	r.mergeServerTools("large", tools)
	m.optionCalls = 0
	server, method, found, err := r.discoveredMCPIdentity(context.Background(), "large/tool123")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "large", server)
	require.Equal(t, "tool123", method)
	require.LessOrEqual(t, m.optionCalls, 2, "unrelated tool aliases must not load configuration")
	m.optionCalls = 0
	_, _, found, err = r.discoveredMCPIdentity(context.Background(), "absent/tool")
	require.NoError(t, err)
	require.False(t, found)
	require.Zero(t, m.optionCalls)
}

func (c *exactIdentityClient) CallTool(_ context.Context, p *schema.CallToolRequestParams, _ ...client.RequestOption) (*schema.CallToolResult, error) {
	c.called = p.Name
	return &schema.CallToolResult{}, nil
}

func TestAdvertisedMCPIdentityUsesLiteralServerAndTool(t *testing.T) {
	for _, tc := range []struct{ server, tool string }{{"datly", "datly.components"}, {"server_name", "tool_with_underscores"}, {"server-name", "tool-with-hyphens"}, {"server.name", "tool.name-with_parts"}, {"server/path", "tool_with-parts.name"}, {"mcp://catalog/nested/service", "literal.tool"}, {"mcp://catalog/nested/service", "nested/tool:operation"}} {
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
			require.Regexp(t, `^[a-zA-Z0-9_-]+$`, alias, "the model sees the existing safe tool alias")
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

func TestAdvertisedMCPIdentityKeepsPrincipalCatalogScoped(t *testing.T) {
	t.Setenv("AGENTLY_MCP_SERVERS", "private")
	r := &Registry{mgr: &principalManager{}, cache: map[string]*toolCacheEntry{}}
	alice := requestctx.WithConversationID(authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"}), "owned")
	bob := requestctx.WithConversationID(authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "bob"}), "owned")
	_, err := r.listServerTools(alice, "private")
	require.NoError(t, err)
	server, method, ok, err := r.discoveredMCPIdentity(alice, "private/secret")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "private", server)
	require.Equal(t, "secret", method)
	_, _, ok, err = r.discoveredMCPIdentity(bob, "private/secret")
	require.NoError(t, err)
	require.False(t, ok)
	_, _, ok, err = r.discoveredMCPIdentity(context.Background(), "private/secret")
	require.NoError(t, err)
	require.False(t, ok)
	require.Empty(t, r.cache)
}
