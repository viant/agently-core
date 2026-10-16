package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/executor"
	"github.com/viant/agently-core/internal/sdkbackend"
	"github.com/viant/agently-core/protocol/mcp/manager"
	identity "github.com/viant/agently-core/protocol/resource"
	auth "github.com/viant/agently-core/service/auth"
	bridge "github.com/viant/agently-core/service/primitiveprovider"
	resources "github.com/viant/agently-core/service/resource"
	window "github.com/viant/agently-core/service/ui/window"
	workspaceui "github.com/viant/agently-core/service/workspace"
	"github.com/viant/agently-core/workspace"
	"github.com/viant/forge/backend/types"
	mcpclient "github.com/viant/mcp/client"
)

func TestENGManagedInternalLayoutScopesDiscoveryAndPreservesRoleVisibility(t *testing.T) {
	root := t.TempDir()
	previous := workspace.Root()
	workspace.SetRoot(root)
	t.Cleanup(func() { workspace.SetRoot(previous) })
	t.Setenv("AGENTLY_WORKSPACE_NO_DEFAULTS", "1")
	t.Setenv("AGENTLY_DB_DRIVER", "sqlite")
	t.Setenv("AGENTLY_DB_DSN", "")
	t.Setenv("AGENTLY_DB_SECRETS", "")
	t.Setenv("AGENTLY_DB_PATH", filepath.Join(t.TempDir(), "runtime.db"))
	t.Setenv("AGENTLY_STATE_PATH", t.TempDir())
	write := func(name, raw string) {
		file := filepath.Join(root, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(file), 0700))
		require.NoError(t, os.WriteFile(file, []byte(raw), 0600))
	}
	keys := []string{"advertiserList", "campaignList", "dealList", "siteListList", "creativeList", "pixelList"}
	var menus strings.Builder
	for _, key := range keys {
		write("extension/forge/windows/"+key+".yaml", fmt.Sprintf("id: %s\nwindowKey: %s\nview: {content: {id: %s, items: []}}\n", key, key, key))
		fmt.Fprintf(&menus, "      - {id: %s, title: %s, action: {type: window, windowKey: %s}}\n", key, key, key)
	}
	write("extension/forge/windows/restricted.yaml", "id: restricted\nwindowKey: restricted\nview: {content: {id: restricted}}\n")
	menus.WriteString("      - {id: restricted, title: Restricted, authorization: {resourceType: window}, visibleWhen: {source: authorization, field: principal.roles, contains: admin}, action: {type: window, windowKey: restricted}}\n      - {id: missing, title: Missing, action: {type: window, windowKey: nonexistentAlias}}\n")
	write("ui/layout.yaml", "version: 1\nid: steward\ntopbar:\n  actions: [{id: automation, title: Automation, action: {type: window, windowKey: schedule}}]\napplications:\n  - id: steward\n    title: Workspace\n    menus:\n"+menus.String())
	write("mcp/unrelated.yaml", "name: unrelated\ntransport: {type: streamable, url: 'https://unrelated.invalid/mcp'}\n")
	var attempts atomic.Int32
	blocked := errors.New("unrelated MCP must not block internal navigation")
	mgr, err := manager.New(manager.NewRepoProvider(), manager.WithClientFactory(func(context.Context, string, string) (mcpclient.Interface, error) {
		attempts.Add(1)
		return nil, blocked
	}))
	require.NoError(t, err)
	t.Cleanup(func() { mgr.CloseConversation("") })
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	rt, err := executor.NewBuilder().WithMCPManager(mgr).WithAgentFinder(apiTestAgentFinder{}).WithModelFinder(apiTestModelFinder{}).WithUIBridge(bridge.NewService(&bridge.Config{})).WithNativeWindowDefaults([]resources.NativeFrameworkWindowBinding{{Resource: window.ResourceBinding{URI: "window://system/schedule", WindowKey: "schedule"}, Load: func(context.Context, *types.WindowTarget) (*types.Window, error) {
		return &types.Window{View: types.View{Content: &types.Container{ID: "schedule"}}}, nil
	}}}).WithSkipRegistryInitialize(true).Build(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { cancel(); require.NoError(t, rt.Close(context.Background())) })
	client, err := sdkbackend.FromRuntime(rt)
	require.NoError(t, err)
	options := APIOptions{Runtime: rt, Client: client, AgentFinder: apiTestAgentFinder{}}
	handler, err := NewAPIHandler(ctx, options)
	require.NoError(t, err)
	signed := auth.InjectUser(context.Background(), "reader")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/workspace/layout", nil).WithContext(signed))
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	for _, key := range append(keys, "schedule") {
		require.Contains(t, response.Body.String(), `"windowKey":"`+key+`"`)
	}
	require.NotContains(t, response.Body.String(), `"windowKey":"restricted"`)
	require.NotContains(t, response.Body.String(), "nonexistentAlias")
	require.Zero(t, attempts.Load())
	// Explicit/custom menu consumers retain their original aggregate catalog path.
	options.MenuReader = workspaceui.NewGatewayLayoutMenuReader(rt.PrimitiveProviders, []workspaceui.GatewayMenuSource{{Resource: identity.ResourceRef{URI: "navigation://workspace/layout"}, ProviderIdentity: resources.InternalProviderIdentity}})
	custom, err := NewAPIHandler(ctx, options)
	require.NoError(t, err)
	response = httptest.NewRecorder()
	custom.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/workspace/layout", nil).WithContext(signed))
	require.Equal(t, http.StatusServiceUnavailable, response.Code)
	require.Positive(t, attempts.Load())
}
