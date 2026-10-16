package tool

import (
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
	authctx "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/internal/auth/mcpauth"
	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcp/manager"
	"github.com/viant/agently-core/runtime/mcpapps"
	mcpschema "github.com/viant/mcp-protocol/schema"
	mcpclient "github.com/viant/mcp/client"
	"testing"
)

type optionalDiscoveryManager struct{ delegatedLinkErrManagerStub }

func (*optionalDiscoveryManager) WithAuthTokenContext(ctx context.Context, _ string) context.Context {
	return ctx
}

func (*optionalDiscoveryManager) Names(context.Context) ([]string, error) {
	return []string{"unlinked-asana", "unlinked-jira"}, nil
}

type discoveryAuthBlocker struct{ calls int }

func (b *discoveryAuthBlocker) AwaitMCPAuth(context.Context, *mcpauth.LinkRequiredError) error {
	b.calls++
	return nil
}
func TestFullCatalogDiscoveryDoesNotDemandUnrelatedOAuthLink(t *testing.T) {
	mgr, err := manager.New(nil)
	require.NoError(t, err)
	reg, err := NewWithManager(mgr)
	require.NoError(t, err)
	reg.mgr = &optionalDiscoveryManager{}
	reg.virtualDefs["safe:read"] = llm.ToolDefinition{Name: "safe:read"}
	blocker := &discoveryAuthBlocker{}
	ctx := mcpauth.WithBlocker(authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"}), blocker)
	defs := reg.DefinitionsWithContext(ctx)
	require.Len(t, defs, 1)
	require.Equal(t, "safe:read", defs[0].Name)
	require.Zero(t, blocker.calls)
	_, err = reg.Execute(ctx, "unlinked-asana/read", nil)
	_, required := mcpauth.FromError(err)
	require.True(t, required, "explicit tool invocation retains link-required behavior")
}

type coldVisibilityClient struct {
	mcpclient.Interface
	calls int
}

func (c *coldVisibilityClient) ListTools(context.Context, *string, ...mcpclient.RequestOption) (*mcpschema.ListToolsResult, error) {
	c.calls++
	return &mcpschema.ListToolsResult{Tools: []mcpschema.Tool{{Name: "helper", Meta: map[string]interface{}{"ui": map[string]interface{}{"visibility": []interface{}{"app"}}}}}}, nil
}
func TestColdFullCatalogPreservesModelVisibilityIdentityAndWarmReuse(t *testing.T) {
	mgr, err := manager.New(nil)
	require.NoError(t, err)
	registry, err := NewWithManager(mgr)
	require.NoError(t, err)
	client := &coldVisibilityClient{}
	registry.internal["service"] = client
	definitions := registry.DefinitionsWithContext(context.Background())
	require.Len(t, definitions, 1)
	visible, err := registry.MCPToolsVisible(context.Background(), []string{"service-helper"}, "model")
	require.NoError(t, err)
	require.False(t, visible["service-helper"])
	app, err := registry.MCPToolsVisible(context.Background(), []string{"service-helper"}, "app")
	require.NoError(t, err)
	require.True(t, app["service-helper"])
	registry.DefinitionsWithContext(context.Background())
	require.Equal(t, 1, client.calls, "warm catalog reads must reuse the existing registry cache")
}

func TestMCPVisibilityNarrowsNativeAndAppDispatch(t *testing.T) {
	for _, tc := range []struct {
		name, visibility string
		host             bool
		allowed          bool
	}{
		{"app excluded from model", `["app"]`, false, false},
		{"app allowed in host", `["app"]`, true, true},
		{"model excluded from host", `["model"]`, true, false},
		{"model allowed", `["model"]`, false, true},
		{"neither", `[]`, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &hostResultClient{}
			registry, _ := newAdvertisedAppCaptureRegistry(client)
			var definition mcpschema.Tool
			require.NoError(t, json.Unmarshal([]byte(`{"name":"tool","inputSchema":{"type":"object"},"_meta":{"ui":{"visibility":`+tc.visibility+`}}}`), &definition))
			registry.replaceServerTools("service", []mcpschema.Tool{definition})
			ctx := protectedTurnContext("turn")
			if tc.host {
				ctx, _ = mcpapps.WithCapture(ctx, "service", "tool", "host-op")
			}
			_, err := registry.Execute(ctx, "service/tool", nil)
			if tc.allowed {
				require.NoError(t, err)
				require.Equal(t, 1, client.calls)
			} else {
				require.Error(t, err)
				require.Zero(t, client.calls)
			}
		})
	}
}

type visibilitySnapshotManager struct {
	outputSchemaManager
	reads int
	tools []mcpschema.Tool
}

func (m *visibilitySnapshotManager) ToolCatalogSnapshot(context.Context, string, string, ...mcpclient.RequestOption) (string, []mcpschema.Tool, error) {
	m.reads++
	return "fixture-generation", m.tools, nil
}
func TestBatchMCPVisibilityReadsPrivateCatalogOnce(t *testing.T) {
	manager := &visibilitySnapshotManager{tools: []mcpschema.Tool{
		{Name: "read", Meta: map[string]interface{}{"ui": map[string]interface{}{"visibility": []interface{}{"model"}}}},
		{Name: "edit", Meta: map[string]interface{}{"ui": map[string]interface{}{"visibility": []interface{}{"app"}}}},
	}}
	manager.policy = &mcpcfg.MCPClient{ToolsListVisibility: mcpcfg.ToolsListVisibilityPrivate}
	registry := newReconnectTestRegistry(&manager.scriptedReconnectManager)
	registry.mgr = manager
	registry.discoveryShared = map[string]discoveryIdentity{"service|fixture": {userID: "alice", catalogSeen: true}}
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
	visible, err := registry.MCPToolsVisible(ctx, []string{"service/read", "service/edit", "native/unknown"}, "model")
	require.NoError(t, err)
	require.True(t, visible["service/read"])
	require.False(t, visible["service/edit"])
	require.True(t, visible["native/unknown"])
	require.Equal(t, 1, manager.reads, "one catalog read per server, not per tool")
	app, err := registry.MCPToolsVisible(ctx, []string{"service/read", "service/edit", "native/unknown"}, "app")
	require.NoError(t, err)
	require.False(t, app["service/read"])
	require.True(t, app["service/edit"])
	require.False(t, app["native/unknown"])
	require.Equal(t, 2, manager.reads)
}

func TestBatchMCPVisibilityRejectsAmbiguousAdvertisedAliases(t *testing.T) {
	registry := &Registry{
		internal: map[string]mcpclient.Interface{"service": nil, "service/nested": nil},
		cache: map[string]*toolCacheEntry{
			"first":  {def: llm.ToolDefinition{Name: "service/nested/tool"}, mcpDef: mcpschema.Tool{Name: "nested/tool"}},
			"second": {def: llm.ToolDefinition{Name: "service/nested/tool"}, mcpDef: mcpschema.Tool{Name: "tool"}},
		},
	}
	_, err := registry.MCPToolsVisible(context.Background(), []string{"service/nested/tool"}, "model")
	require.ErrorContains(t, err, "ambiguous advertised MCP tool alias")
}

type partialLookupCatalogClient struct {
	mcpclient.Interface
	calls int
}

func (c *partialLookupCatalogClient) ListTools(context.Context, *string, ...mcpclient.RequestOption) (*mcpschema.ListToolsResult, error) {
	c.calls++
	return &mcpschema.ListToolsResult{Tools: []mcpschema.Tool{{Name: "read"}, {Name: "helper", Meta: map[string]interface{}{"ui": map[string]interface{}{"visibility": []interface{}{"app"}}}}}}, nil
}
func TestExactInternalLookupDoesNotTruncateLaterFullCatalog(t *testing.T) {
	mgr, err := manager.New(nil)
	require.NoError(t, err)
	registry, err := NewWithManager(mgr)
	require.NoError(t, err)
	client := &partialLookupCatalogClient{}
	registry.internal["service"] = client
	definition, found := registry.GetDefinitionWithContext(context.Background(), "service:read")
	require.True(t, found)
	require.NotNil(t, definition)
	definitions := registry.DefinitionsWithContext(context.Background())
	require.Len(t, definitions, 2, "an exact lookup must not turn the full catalog into its subset")
	visible, err := registry.MCPToolsVisible(context.Background(), []string{"service-helper", "service-read"}, "model")
	require.NoError(t, err)
	require.False(t, visible["service-helper"])
	require.True(t, visible["service-read"])
	registry.DefinitionsWithContext(context.Background())
	require.Equal(t, 1, client.calls, "reuse the already fetched complete internal catalog")
}
