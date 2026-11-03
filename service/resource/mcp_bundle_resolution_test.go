package resource

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
	authctx "github.com/viant/agently-core/internal/auth"
	registry "github.com/viant/agently-core/internal/tool/registry"
	cfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcp/manager"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/agently-core/protocol/tool/bundle"
	fsstore "github.com/viant/agently-core/workspace/store/fs"
	"github.com/viant/mcp"
	"github.com/viant/mcp-protocol/schema"
	mcpclient "github.com/viant/mcp/client"
)

type bundleCatalogClient struct {
	mcpclient.Interface
	tools []schema.Tool
	calls []string
}

func (c *bundleCatalogClient) ListTools(context.Context, *string, ...mcpclient.RequestOption) (*schema.ListToolsResult, error) {
	return &schema.ListToolsResult{Tools: c.tools}, nil
}
func (c *bundleCatalogClient) CallTool(_ context.Context, request *schema.CallToolRequestParams, _ ...mcpclient.RequestOption) (*schema.CallToolResult, error) {
	c.calls = append(c.calls, request.Name)
	return &schema.CallToolResult{}, nil
}

func TestPublishedMCPConnectionBundlePreservesURIAndLiteralMethods(t *testing.T) {
	actor, verify := PublicReadActors()
	internal := &referenceCatalog{namespace: "workspace", owner: "configuration", kind: "mcp", rows: map[string]map[string]json.RawMessage{}, denied: map[string]bool{}}
	publisher := &referenceCatalog{namespace: "inventory", owner: "inventory-owner", kind: "mcp", rows: map[string]map[string]json.RawMessage{}, denied: map[string]bool{}, uriByName: map[string]string{"logical": "mcp://inventory/nested/connection", "separate": "mcp://inventory/nested_connection"}}
	internal.set("", "publisher", referenceConfig("publisher", "inventory-owner", cfg.ResourceCacheUser))
	for _, name := range []string{"logical", "separate"} {
		definition := referenceConfig("handshake-only", "", cfg.ResourceCacheUser)
		definition.ToolsListVisibility = cfg.ToolsListVisibilityPrivate
		publisher.set("alice", name, definition)
	}
	remote := &bundleCatalogClient{tools: []schema.Tool{{Name: "ReadItem"}, {Name: "literal.tool"}, {Name: "nested/tool:operation"}}}
	separate := &bundleCatalogClient{tools: []schema.Tool{{Name: "OtherItem"}}}
	mgr, err := manager.New(manager.NewRepoProvider(manager.WithRepoStateStore(fsstore.NewStateStore(t.TempDir()))), manager.WithClientFactory(func(_ context.Context, _, name string) (mcpclient.Interface, error) {
		switch name {
		case "publisher":
			return referenceClient(t, publisher)
		case "mcp://inventory/nested/connection":
			return remote, nil
		case "mcp://inventory/nested_connection":
			return separate, nil
		default:
			return nil, fmt.Errorf("unexpected connection %q", name)
		}
	}))
	require.NoError(t, err)
	defer mgr.CloseConversation("")
	require.NoError(t, mgr.RegisterLocal(context.Background(), "configuration", &cfg.MCPClient{ClientOptions: &mcp.ClientOptions{}, PrimitiveProviderIdentity: "configuration", ResourceCacheScopes: map[string]string{"workspace": cfg.ResourceCachePublic}}, func(context.Context) (mcpclient.Interface, error) { return referenceClient(t, internal) }))
	gateway := NewGateway(mgr, actor, verify, "bundle-reader")
	defer gateway.Close()
	reader, err := NewMCPReferenceReader(gateway, MCPDefinitionSource{ConnectionName: "configuration", ProviderIdentity: "configuration", Namespace: "workspace"}, []identity.ResourceRef{{URI: "mcp://workspace/publisher"}})
	require.NoError(t, err)
	_, err = mgr.BindDefinitionReader(reader)
	require.NoError(t, err)
	reg, err := registry.NewWithManager(mgr)
	require.NoError(t, err)
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
	names, err := reader.List(ctx)
	require.NoError(t, err)
	require.Contains(t, names, "mcp://inventory/nested/connection")
	for _, method := range []string{"ReadItem", "literal.tool", "nested/tool:operation"} {
		exact := "mcp://inventory/nested/connection:" + method
		// Establish that the owning registry already understands this explicit URI.
		defs, matchErr := reg.MatchDefinitionWithContextResult(ctx, exact)
		require.NoError(t, matchErr)
		require.Len(t, defs, 1)
		var failures []error
		resolved := bundle.ResolveDefinitions(&bundle.Bundle{ID: "items", Match: []llm.Tool{{Name: exact}}}, func(pattern string) []*llm.ToolDefinition {
			definitions, matchErr := reg.MatchDefinitionWithContextResult(ctx, pattern)
			if matchErr != nil {
				failures = append(failures, matchErr)
			}
			return definitions
		})
		require.Empty(t, failures, "bundle expansion must not invent additional connection names")
		require.Len(t, resolved, 1)
		require.Equal(t, "mcp://inventory/nested/connection", resolved[0].SourceMCPServer)
		_, err = reg.ExecuteExactJSON(ctx, exact, map[string]interface{}{}, "")
		require.NoError(t, err)
	}
	require.Equal(t, []string{"ReadItem", "literal.tool", "nested/tool:operation"}, remote.calls)
	resolved := bundle.ResolveDefinitions(&bundle.Bundle{Match: []llm.Tool{{Name: "mcp://inventory/nested/connection:*", Exclude: []string{"mcp://inventory/nested/connection:literal.tool"}}}}, func(pattern string) []*llm.ToolDefinition {
		definitions, matchErr := reg.MatchDefinitionWithContextResult(ctx, pattern)
		require.NoError(t, matchErr)
		return definitions
	})
	require.Len(t, resolved, 2)
	for _, definition := range resolved {
		require.NotEqual(t, "mcp://inventory/nested/connection/literal.tool", definition.Name)
	}
	var failures []error
	missing := bundle.ResolveDefinitions(&bundle.Bundle{Match: []llm.Tool{{Name: "mcp://inventory/absent:ReadItem"}}}, func(pattern string) []*llm.ToolDefinition {
		definitions, matchErr := reg.MatchDefinitionWithContextResult(ctx, pattern)
		if matchErr != nil {
			failures = append(failures, matchErr)
		}
		return definitions
	})
	require.Empty(t, missing)
	require.Len(t, failures, 1, "the owning lookup failure is retained without invented aliases")

	defs, err := reg.MatchDefinitionWithContextResult(ctx, "mcp://inventory/nested_connection:OtherItem")
	require.NoError(t, err)
	require.Len(t, defs, 1)
	require.Equal(t, "mcp://inventory/nested_connection", defs[0].SourceMCPServer)
}
