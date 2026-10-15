package tool

import (
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
	authctx "github.com/viant/agently-core/internal/auth"
	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/runtime/mcpapps"
	mcpschema "github.com/viant/mcp-protocol/schema"
	mcpclient "github.com/viant/mcp/client"
	"testing"
)

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
