package workspace

import (
	"context"
	"encoding/json"
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

type standardNavigationClient struct{ mcpclient.Interface }

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
func (*standardNavigationClient) ReadResource(_ context.Context, r *schema.ReadResourceRequestParams, _ ...mcpclient.RequestOption) (*schema.ReadResourceResult, error) {
	// Ordinary authorized MCP content has no custom MenuProjection receipt.
	return &schema.ReadResourceResult{Contents: []schema.ReadResourceResultContentsElem{{Uri: r.Uri, Text: `{"applications":[{"id":"management","title":"Management","icon":"database","menus":[{"id":"edit","title":"Edit","action":{"type":"url","url":"https://studio.example/"}}]}]}`}}}, nil
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
