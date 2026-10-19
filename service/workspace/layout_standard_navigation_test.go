package workspace

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	cfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcp/manager"
	identity "github.com/viant/agently-core/protocol/resource"
	resources "github.com/viant/agently-core/service/resource"
	"github.com/viant/mcp"
	"github.com/viant/mcp-protocol/schema"
	mcpclient "github.com/viant/mcp/client"
)

type standardNavigationClient struct {
	mcpclient.Interface
	mu         sync.Mutex
	definition string
	reads      int
}

const validStandardNavigation = `{"applications":[{"id":"management","title":"Management","icon":"database","menus":[{"id":"edit","title":"Edit","action":{"type":"url","url":"https://studio.example/"}}]}]}`

func (*standardNavigationClient) Close() error { return nil }
func (*standardNavigationClient) Initialize(context.Context, ...mcpclient.RequestOption) (*schema.InitializeResult, error) {
	return &schema.InitializeResult{Capabilities: schema.ServerCapabilities{Resources: &schema.ServerCapabilitiesResources{}, Extensions: map[string]map[string]interface{}{
		"io.viant/resources/filter": {"schemaVersion": 1, "fields": []string{"namespace", "kind"}},
	}}}, nil
}
func (*standardNavigationClient) ListTools(context.Context, *string, ...mcpclient.RequestOption) (*schema.ListToolsResult, error) {
	return &schema.ListToolsResult{Tools: []schema.Tool{{Name: "namespaces/list"}}}, nil
}
func (*standardNavigationClient) CallTool(context.Context, *schema.CallToolRequestParams, ...mcpclient.RequestOption) (*schema.CallToolResult, error) {
	return &schema.CallToolResult{StructuredContent: map[string]any{"namespaces": []any{map[string]any{"uri": "ns://external", "name": "External"}}}}, nil
}
func (*standardNavigationClient) ListResourcesWithParams(context.Context, *schema.ListResourcesRequestParams, ...mcpclient.RequestOption) (*schema.ListResourcesResult, error) {
	return &schema.ListResourcesResult{Resources: []schema.Resource{{Uri: "navigation://external/main", Name: "Management"}}}, nil
}
func (c *standardNavigationClient) ReadResource(_ context.Context, r *schema.ReadResourceRequestParams, _ ...mcpclient.RequestOption) (*schema.ReadResourceResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reads++
	raw := c.definition
	if raw == "" {
		raw = validStandardNavigation
	}
	// Ordinary authorized MCP content has no custom MenuProjection receipt.
	return &schema.ReadResourceResult{Contents: []schema.ReadResourceResultContentsElem{{Uri: r.Uri, Text: raw}}}, nil
}

func TestWorkspaceMenuRecoversRejectedPublicationAndCachesCorrection(t *testing.T) {
	ctx := context.Background()
	client := &standardNavigationClient{definition: `{"applications":[{"id":"management","title":"Management","menus":[{"id":"group","title":"Group","action":{"type":"url","url":"https://studio.example/"},"children":[{"id":"edit","title":"Edit","action":{"type":"url","url":"https://studio.example/"}}]}]}]}`}
	mgr, err := manager.New(nil)
	require.NoError(t, err)
	require.NoError(t, mgr.RegisterLocal(ctx, "external", &cfg.MCPClient{ClientOptions: &mcp.ClientOptions{}, PrimitiveProviderIdentity: "external", ResourceCacheScopes: map[string]string{"*": cfg.ResourceCachePublic}}, func(context.Context) (mcpclient.Interface, error) { return client, nil }))
	t.Cleanup(func() { mgr.CloseConversation("") })
	actor, verify := resources.PublicReadActors()
	gateway := resources.NewGateway(mgr, actor, verify, "host")
	t.Cleanup(func() { gateway.Close() })
	reader := NewGatewayLayoutMenuReader(gateway, []GatewayMenuSource{{Resource: identity.ResourceRef{URI: "navigation://external/main"}, ProviderIdentity: "external"}})
	rejected, err := reader(ctx)
	if rejected != nil {
		_ = rejected.Close()
	}
	require.ErrorContains(t, err, "either action or children")
	client.mu.Lock()
	client.definition = validStandardNavigation
	client.mu.Unlock()
	// No notification is needed to recover an entry rejected as invalid.
	for i := 0; i < 2; i++ {
		read, err := reader(ctx)
		require.NoError(t, err)
		require.NoError(t, read.Revalidate(ctx))
		require.NoError(t, read.Close())
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	require.Equal(t, 2, client.reads, "the corrected publication stays cached for warm reads")
}

func TestWorkspaceMenuAcceptsStandardResourceWithoutLegacyProjection(t *testing.T) {
	ctx := context.Background()
	mgr, err := manager.New(nil)
	require.NoError(t, err)
	require.NoError(t, mgr.RegisterLocal(ctx, "external", &cfg.MCPClient{ClientOptions: &mcp.ClientOptions{}, PrimitiveProviderIdentity: "external"}, func(context.Context) (mcpclient.Interface, error) { return &standardNavigationClient{}, nil }))
	t.Cleanup(func() { mgr.CloseConversation("") })
	actor, verify := resources.PublicReadActors()
	gateway := resources.NewGateway(mgr, actor, verify, "host")
	t.Cleanup(func() { gateway.Close() })
	reader := NewGatewayLayoutMenuReader(gateway, []GatewayMenuSource{{Resource: identity.ResourceRef{URI: "navigation://external/main"}, ProviderIdentity: "external"}})
	read, err := reader(ctx)
	require.NoError(t, err)
	var apps []LayoutApplication
	require.NoError(t, json.Unmarshal(read.Applications, &apps))
	require.Len(t, apps, 1)
	require.Equal(t, "management", apps[0].ID)
	require.NoError(t, read.Revalidate(ctx))
	require.NoError(t, read.Close())
}
