package tool

import (
	"context"
	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/runtime/discovery"
	schema "github.com/viant/mcp-protocol/schema"
	client "github.com/viant/mcp/client"
	"sync/atomic"
	"testing"
	"time"
)

type budgetDiscoveryClient struct {
	discoveryListClient
	attempts atomic.Int32
}

func (c *budgetDiscoveryClient) ListTools(ctx context.Context, cursor *string, opts ...client.RequestOption) (*schema.ListToolsResult, error) {
	if c.attempts.Add(1) == 1 {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return &schema.ListToolsResult{Tools: []schema.Tool{{Name: "read"}}}, nil
}
func TestExpiredOptionalDiscoveryBudgetDoesNotBlockRequiredPrivateDiscovery(t *testing.T) {
	c := &budgetDiscoveryClient{}
	mgr := &discoveryManagerStub{options: &config.MCPClient{ToolsListVisibility: config.ToolsListVisibilityPrivate}, getFunc: func(string, string) (client.Interface, error) { return c, nil }, reconnectFunc: func(string, string) (client.Interface, error) { return c, nil }}
	r := &Registry{mgr: mgr, cache: map[string]*toolCacheEntry{}, discoverySurfaceTimeout: 20 * time.Millisecond, discoveryTimeout: time.Second}
	ctx := discoveryUserContext()
	_, _ = r.listServerTools(discovery.MergeMode(ctx, discovery.Mode{ToolSurface: true}), "private")
	require.Empty(t, mgr.reconnectCallsSnapshot(), "expired discovery must not evict a session using a canceled reconnect")
	require.Empty(t, r.discoveryFailUntil, "an exhausted caller budget is not evidence that the user's provider is unavailable")
	tools, err := r.listServerTools(discovery.MergeMode(ctx, discovery.Mode{ToolSurface: true, Required: true}), "private")
	require.NoError(t, err)
	require.Len(t, tools, 1)
}
