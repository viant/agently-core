package tool

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	authctx "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/protocol/mcpname"
	"github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/jsonrpc"
	"github.com/viant/mcp-protocol/schema"
	"github.com/viant/mcp/client"
	"github.com/viant/mcp/client/catalog"
)

type countedDispatchClient struct {
	discoveryListClient
	calls, lists int
	revoked      bool
}

func (c *countedDispatchClient) ListTools(ctx context.Context, cursor *string, opts ...client.RequestOption) (*schema.ListToolsResult, error) {
	c.lists++
	return c.discoveryListClient.ListTools(ctx, cursor, opts...)
}
func (c *countedDispatchClient) CallTool(ctx context.Context, p *schema.CallToolRequestParams, opts ...client.RequestOption) (*schema.CallToolResult, error) {
	c.calls++
	if c.revoked {
		return nil, jsonrpc.NewHTTPStatusError(http.StatusForbidden)
	}
	return c.discoveryListClient.CallTool(ctx, p, opts...)
}

func TestWarmPrivateToolExecutesWithoutCatalogPreflight(t *testing.T) {
	for _, name := range []string{"service:read", mcpname.Canonical("service/read")} {
		t.Run(name, func(t *testing.T) {
			c := &countedDispatchClient{discoveryListClient: discoveryListClient{tools: []schema.Tool{{Name: "read"}}}}
			page, err := catalog.New([]byte(`{"tools":[{"name":"read","inputSchema":{"type":"object"}}]}`), "service")
			require.NoError(t, err)
			manager := &metadataProjectionManager{page: page}
			manager.getFunc = func(string, string) (client.Interface, error) { return c, nil }
			r := &Registry{mgr: manager, cache: map[string]*toolCacheEntry{}}
			ctx := requestctx.WithConversationID(authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"}), "example-turn")
			_, err = r.listServerTools(ctx, "service")
			require.NoError(t, err)
			require.Positive(t, c.lists)
			before := c.lists
			_, err = r.Execute(ctx, name, nil)
			require.NoError(t, err)
			require.Equal(t, 1, c.calls)
			require.Equal(t, before, c.lists)
			c.revoked = true
			_, err = r.Execute(ctx, name, nil)
			require.Error(t, err)
			require.Equal(t, 2, c.calls)
			require.Equal(t, before, c.lists)
			require.Empty(t, manager.reconnectCalls)
		})
	}
}
