package resource

import (
	"context"

	"github.com/viant/agently-core/protocol/mcp/manager"
	"github.com/viant/mcp-protocol/schema"
	mcpclient "github.com/viant/mcp/client"
)

// gatewayAuthClient binds each operation's current credential. The underlying
// manager client may be cached, but this wrapper never retains a request token.
type gatewayAuthClient struct {
	mcpclient.Interface
	manager *manager.Manager
	name    string
}

func (c *gatewayAuthClient) ListTools(ctx context.Context, cursor *string, options ...mcpclient.RequestOption) (*schema.ListToolsResult, error) {
	return c.Interface.ListTools(c.manager.WithAuthTokenContext(ctx, c.name), cursor, options...)
}

func (c *gatewayAuthClient) CallTool(ctx context.Context, params *schema.CallToolRequestParams, options ...mcpclient.RequestOption) (*schema.CallToolResult, error) {
	return c.Interface.CallTool(c.manager.WithAuthTokenContext(ctx, c.name), params, options...)
}
