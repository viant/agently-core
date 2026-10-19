package workspace

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	cfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcp/manager"
	identity "github.com/viant/agently-core/protocol/resource"
	resources "github.com/viant/agently-core/service/resource"
	"github.com/viant/mcp"
	mcpclient "github.com/viant/mcp/client"
)

func TestApplicationSourcesUseProviderAppsInWorkspaceOrder(t *testing.T) {
	ctx := context.Background()
	actor, verify := resources.PublicReadActors()
	mgr, err := manager.New(nil)
	require.NoError(t, err)
	t.Cleanup(func() { mgr.CloseConversation("") })
	for _, name := range []string{"first", "second"} {
		root := t.TempDir()
		raw := []byte(`applications:
  - id: primary
    title: Provider primary
    icon: chart
    menus: [{id: open, title: Open, action: {type: window, windowKey: orders}}]
  - id: secondary
    title: Provider secondary
    icon: database
    menus: [{id: open, title: Open, action: {type: window, windowKey: orders}}]
`)
		require.NoError(t, os.WriteFile(filepath.Join(root, "layout.yaml"), raw, 0600))
		def, err := resources.SnapshotLayoutMenuDefinition("layout.yaml", "navigation://"+name+"/main")
		require.NoError(t, err)
		snapshot, err := resources.NewNativeAssetSnapshot(ctx, root, []resources.NativeSnapshotDefinition{def})
		require.NoError(t, err)
		bindings, err := snapshot.Bindings(name, func(_ context.Context, a identity.VerifiedActor, _ identity.ResourceURI, _ string) (identity.ResourceRevisionPolicy, error) {
			return capturedMenuPolicy{a}, nil
		})
		require.NoError(t, err)
		provider, err := resources.NewLocalProvider(resources.LocalConfig{ProviderIdentity: name, Actor: actor, Verify: verify, Bindings: bindings,
			Authorize:  func(context.Context, identity.VerifiedActor, identity.ResourceURI, string) error { return nil },
			Validators: map[string]resources.LocalResourceValidator{"navigation": resources.ValidateMenu},
			MenuActionVisibility: func(context.Context, identity.VerifiedActor, identity.ResourceURI, json.RawMessage) (bool, error) {
				return true, nil
			},
		})
		require.NoError(t, err)
		require.NoError(t, mgr.RegisterLocal(ctx, name, &cfg.MCPClient{ClientOptions: &mcp.ClientOptions{}, PrimitiveProviderIdentity: name}, func(context.Context) (mcpclient.Interface, error) { return resources.NewLocalMCPClient(provider) }))
	}
	gateway := resources.NewGateway(mgr, actor, verify, "host")
	t.Cleanup(func() { gateway.Close() })
	page, err := resources.ListPageFrom[json.RawMessage](ctx, gateway, []resources.Connection{{Name: "first"}, {Name: "second"}}, "navigation", resources.CatalogQuery{Limit: 100}, false)
	require.NoError(t, err)
	require.Len(t, page.Items, 2)
	reader, err := ApplicationMenuReader(gateway, []byte(`applicationSources:
 - {connection: second, resource: "navigation://second/main", applicationId: secondary}
 - {connection: first, resource: "navigation://first/absent", applicationId: hidden}
 - {connection: first, resource: "navigation://first/main", applicationId: primary}
applications: []
`))
	require.NoError(t, err)
	for i := 0; i < 2; i++ {
		read, err := reader(ctx)
		require.NoError(t, err)
		var apps []LayoutApplication
		require.NoError(t, json.Unmarshal(read.Applications, &apps))
		require.Len(t, apps, 2)
		require.Equal(t, "secondary", apps[0].ID)
		require.Equal(t, "Provider secondary", apps[0].Title)
		require.Equal(t, "database", apps[0].Icon)
		require.Equal(t, "primary", apps[1].ID)
		require.NoError(t, read.Revalidate(ctx))
		require.NoError(t, read.Close())
	}
}

func TestApplicationSourcesRejectInvalidConfiguration(t *testing.T) {
	for _, raw := range []string{
		`applicationSources: [{connection: server, resource: "window://remote/main", applicationId: app}]`,
		`applicationSources: [{connection: server, resource: "navigation://remote/main"}]`,
		`applicationSources: [{resource: "navigation://remote/main", applicationId: app}]`,
	} {
		_, err := ApplicationMenuReader(nil, []byte(raw))
		require.Error(t, err)
	}
}
