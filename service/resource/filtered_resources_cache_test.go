package resource

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	cfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcp/manager"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/mcp"
	"github.com/viant/mcp-protocol/schema"
	mcpclient "github.com/viant/mcp/client"
)

// The provider deliberately has no namespaces/get or resource/get. Resource
// discovery must use the advertised standard-method filter extension instead.
type filteredResourcesClient struct {
	mcpclient.Interface
	fixtureNotificationPort
	t                            *testing.T
	lists, namespaceLists, reads int
}

func (c *filteredResourcesClient) Initialize(context.Context, ...mcpclient.RequestOption) (*schema.InitializeResult, error) {
	return &schema.InitializeResult{Capabilities: schema.ServerCapabilities{
		Resources: &schema.ServerCapabilitiesResources{},
		Extensions: map[string]map[string]interface{}{"io.viant/resources/filter": {
			"schemaVersion": 1, "fields": []string{"namespace", "kind", "limit"}, "pagination": "cursor",
		}},
	}}, nil
}

func (c *filteredResourcesClient) ListTools(context.Context, *string, ...mcpclient.RequestOption) (*schema.ListToolsResult, error) {
	return &schema.ListToolsResult{Tools: []schema.Tool{{Name: "namespaces/list"}}}, nil
}

func (c *filteredResourcesClient) CallTool(_ context.Context, r *schema.CallToolRequestParams, _ ...mcpclient.RequestOption) (*schema.CallToolResult, error) {
	require.Equal(c.t, "namespaces/list", r.Name, "no namespace details or custom resource-get calls")
	c.namespaceLists++
	return &schema.CallToolResult{StructuredContent: map[string]any{"namespaces": []any{
		map[string]any{"uri": "ns://collection-id", "name": "Workspace display name"},
	}}}, nil
}

func (c *filteredResourcesClient) ListResourcesWithParams(_ context.Context, r *schema.ListResourcesRequestParams, _ ...mcpclient.RequestOption) (*schema.ListResourcesResult, error) {
	c.lists++
	raw, err := json.Marshal(r.Meta)
	require.NoError(c.t, err)
	var meta map[string]any
	require.NoError(c.t, json.Unmarshal(raw, &meta))
	filter, ok := meta["io.viant/resources/filter"].(map[string]any)
	require.True(c.t, ok)
	require.Equal(c.t, "ns://collection-id", filter["namespace"])
	if r.Cursor == nil {
		cursor := "page-two"
		return &schema.ListResourcesResult{NextCursor: &cursor, Resources: []schema.Resource{{Uri: "window://remote/orders", Name: "Orders"}}}, nil
	}
	require.Equal(c.t, "page-two", *r.Cursor)
	return &schema.ListResourcesResult{Resources: []schema.Resource{{Uri: "navigation://remote/main", Name: "Operations"}}}, nil
}

func (c *filteredResourcesClient) ReadResource(_ context.Context, r *schema.ReadResourceRequestParams, _ ...mcpclient.RequestOption) (*schema.ReadResourceResult, error) {
	c.reads++
	require.Equal(c.t, "window://remote/orders", r.Uri)
	return &schema.ReadResourceResult{Contents: []schema.ReadResourceResultContentsElem{{Uri: r.Uri, Text: `{"title":"Orders","view":{"content":{"id":"orders","text":"Orders"}},"dataSource":{}}`}}}, nil
}

func TestFilteredStandardResourceDiscoveryRetainsGatewayCacheAndNotifications(t *testing.T) {
	ctx := context.Background()
	client := &filteredResourcesClient{t: t}
	mgr, err := manager.New(nil)
	require.NoError(t, err)
	require.NoError(t, mgr.RegisterLocal(ctx, "external", &cfg.MCPClient{
		ClientOptions: &mcp.ClientOptions{}, PrimitiveProviderIdentity: "external-owner",
	}, func(context.Context) (mcpclient.Interface, error) { return client, nil }))
	t.Cleanup(func() { mgr.CloseConversation("") })
	actor, verify := PublicReadActors()
	gateway := NewGateway(mgr, actor, verify, "host")
	t.Cleanup(func() { gateway.Close() })

	for i := 0; i < 3; i++ {
		rows, err := gateway.List(ctx, "window", "remote")
		require.NoError(t, err)
		require.Len(t, rows, 1)
		if i == 0 {
			_, err = gateway.Get(ctx, rows[0].Connection, identity.ResourceRef{URI: rows[0].Resource.URI}, nil)
			require.NoError(t, err)
		}
	}
	require.Equal(t, 1, client.namespaceLists, "warm lists must not rediscover namespaces")
	require.Equal(t, 2, client.lists, "the two resource pages populate the existing cache once")
	require.Equal(t, 1, client.reads, "listing must not hydrate every definition")
	client.resourceChanged()
	rows, err := gateway.List(ctx, "window", "remote")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, 4, client.lists, "resource notification invalidates the existing inventory cache")
}

func TestFilteredStandardResourceDiscoveryKeepsPrivateInventoriesPerActor(t *testing.T) {
	type principalKey struct{}
	deadline := time.Now().Add(time.Hour)
	resolve := func(ctx context.Context) (identity.VerifiedActor, error) {
		subject, _ := ctx.Value(principalKey{}).(string)
		return identity.VerifiedActor{Issuer: "https://fixture.invalid", Subject: subject,
			TenantID: "tenant", AccountID: subject, IdentityRevision: "current", ValidUntil: deadline}, nil
	}
	verify := func(ctx context.Context, held identity.VerifiedActor) error {
		current, _ := resolve(ctx)
		if current != held {
			return identity.ErrResourceDenied
		}
		return nil
	}
	client := &filteredResourcesClient{t: t}
	mgr, err := manager.New(nil)
	require.NoError(t, err)
	require.NoError(t, mgr.RegisterLocal(context.Background(), "private", &cfg.MCPClient{
		ClientOptions: &mcp.ClientOptions{}, PrimitiveProviderIdentity: "private-owner",
		ResourceCacheScopes: map[string]string{"*": cfg.ResourceCacheUser},
	}, func(context.Context) (mcpclient.Interface, error) { return client, nil }))
	t.Cleanup(func() { mgr.CloseConversation("") })
	gateway := NewGateway(mgr, resolve, verify, "host")
	t.Cleanup(func() { gateway.Close() })
	for i, subject := range []string{"alice", "bob", "alice"} {
		ctx := context.WithValue(context.Background(), principalKey{}, subject)
		rows, err := gateway.List(ctx, "window", "remote")
		require.NoError(t, err)
		require.Len(t, rows, 1)
		want := []int{2, 4, 4}
		require.Equal(t, want[i], client.lists, "private inventories populate once per actor")
	}
}
