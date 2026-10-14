package executor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/protocol/mcp/manager"
	identity "github.com/viant/agently-core/protocol/resource"
	auth "github.com/viant/agently-core/service/auth"
	forge "github.com/viant/agently-core/service/primitiveprovider"
	resources "github.com/viant/agently-core/service/resource"
	workspaceui "github.com/viant/agently-core/service/workspace"
	"github.com/viant/agently-core/workspace"
)

func TestDefaultZeroExpiryMetadataPreservesLayoutAndWindowLookup(t *testing.T) {
	root := t.TempDir()
	old := workspace.Root()
	workspace.SetRoot(root)
	t.Cleanup(func() { workspace.SetRoot(old) })
	write := func(name, raw string) {
		file := filepath.Join(root, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(file), 0700))
		require.NoError(t, os.WriteFile(file, []byte(raw), 0600))
	}
	write("ui/layout.yaml", "version: 1\nid: original\napplications:\n  - id: workspace\n    title: Workspace\n    menus: [{id: orders, title: Orders, action: {type: window, windowKey: order}}]\n")
	write("extension/forge/windows/order.yaml", "id: order\nwindowKey: order\ntitle: Original Order\nquickSearch: {dataSourceRef: advertiser_lookup, field: Name}\nview:\n  content:\n    id: order\n    items:\n      - id: advertiser\n        type: text\n        lookup: {dataSource: advertiser_lookup, display: '${Name}', queryInput: Name, resolveInput: Id, outputs: []}\n")
	write("extension/forge/datasources/advertiser_lookup.yaml", "id: advertiser_lookup\nselectors: {data: rows}\nbackend: {kind: inline, rows: [{Id: 1, Name: Original}]}\n")
	mgr, err := manager.New(nil)
	require.NoError(t, err)
	rt := &Runtime{MCPManager: mgr, AuthConfig: &auth.Config{Enabled: true}, UIBridge: forge.NewService(&forge.Config{})}
	require.NoError(t, NewBuilder().configurePrimitiveProviders(rt))
	defer rt.PrimitiveProviders.Close()
	ctx := auth.InjectUser(context.Background(), "awitas")
	actor, err := rt.PrimitiveProviders.Actor(ctx)
	require.NoError(t, err)
	require.True(t, actor.PublicRead)
	require.True(t, actor.ValidUntil.IsZero(), "real default metadata has no synthesized TTL")
	handler := workspaceui.NewMetadataHandler(nil, nil, "fixture")
	source := rt.NativeLayoutSource
	require.NoError(t, handler.SetCapturedLayout(source.Root, source.Source, source.Raw, source.Check))
	handler.SetWindowResourceCatalog(rt.UIBridge)
	handler.SetLayoutMenuReader(workspaceui.NewGatewayLayoutMenuReader(rt.PrimitiveProviders, []workspaceui.GatewayMenuSource{{Resource: identity.ResourceRef{URI: "menu://workspace/layout"}, ProviderIdentity: resources.InternalProviderIdentity}}))
	mux := http.NewServeMux()
	handler.Register(mux)
	for i := 0; i < 2; i++ {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/workspace/layout", nil).WithContext(ctx))
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		require.Contains(t, response.Body.String(), "Workspace")
		require.Contains(t, response.Body.String(), "Orders")
		scoped, finish, err := rt.PrimitiveProviders.BeginReadSequence(ctx, nil)
		require.NoError(t, err)
		inventory, err := resources.NewWindowViewReader(rt.PrimitiveProviders).LoadAll(scoped)
		require.NoError(t, err)
		require.Len(t, inventory, 1)
		require.Equal(t, "order", inventory[0].ID)
		require.Equal(t, "advertiser_lookup", inventory[0].QuickSearch.DataSourceRef)
		opened, err := rt.UIBridge.WindowDefinitionGet(scoped, &forge.WindowDefinitionGetInput{WindowID: "order"})
		require.NoError(t, err)
		require.Equal(t, "advertiser_lookup", opened.Definition.View.Content.Items[0].Lookup.DataSource)
		require.NoError(t, finish(), "successful zero-expiry menu/window inventory must release")
	}
	_, err = handler.Layout(context.Background(), nil)
	require.Error(t, err, "anonymous callers still cannot consume the auth-enabled source")
}
