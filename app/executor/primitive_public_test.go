package executor

import (
	"context"
	"github.com/stretchr/testify/require"
	ds "github.com/viant/agently-core/protocol/datasource"
	"github.com/viant/agently-core/protocol/mcp/manager"
	primitive "github.com/viant/agently-core/protocol/primitive"
	auth "github.com/viant/agently-core/service/auth"
	resources "github.com/viant/agently-core/service/resource"
	"github.com/viant/agently-core/workspace"
	fsstore "github.com/viant/agently-core/workspace/store/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultPublicFeedUsesOneNativeGateway(t *testing.T) {
	root := t.TempDir()
	old := workspace.Root()
	workspace.SetRoot(root)
	t.Cleanup(func() { workspace.SetRoot(old) })
	require.NoError(t, os.MkdirAll(filepath.Join(root, "feeds"), 0700))
	file := filepath.Join(root, "feeds/media.yaml")
	body := []byte("id: media-plan\nmatch: {service: synthetic, method: update}\nactivation: {scope: turn}\ndataSource: {draft: {source: output}}\nui: {title: Original, controls: {draftOnly: true}}\n")
	require.NoError(t, os.WriteFile(file, body, 0600))
	mgr, err := manager.New(nil)
	require.NoError(t, err)
	out := &Runtime{MCPManager: mgr, AuthConfig: &auth.Config{Enabled: false}}
	b := NewBuilder()
	require.NoError(t, b.configurePrimitiveProviders(out))
	defer out.PrimitiveProviders.Close()
	require.NotNil(t, out.LocalPrimitiveProvider)
	require.True(t, mgr.IsLocalClient("internal"))
	originalGateway := out.PrimitiveProviders
	originalProvider := out.LocalPrimitiveProvider
	require.NoError(t, b.configurePrimitiveProviders(out))
	require.Same(t, originalGateway, out.PrimitiveProviders)
	require.Same(t, originalProvider, out.LocalPrimitiveProvider)
	ctx := context.Background()
	page, err := resources.ListPage[primitive.ToolfeedDefinition](ctx, out.PrimitiveProviders, primitive.ToolfeedKind, resources.CatalogQuery{}, true)
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	got, err := resources.ResolveDefinition[primitive.ToolfeedDefinition](ctx, out.PrimitiveProviders, primitive.ToolfeedKind, "media-plan", func(v primitive.ToolfeedDefinition) string { return v.ID })
	require.NoError(t, err)
	require.Equal(t, "toolfeed://workspace/media", got.Resource.URI)
	require.JSONEq(t, `{"title":"Original","controls":{"draftOnly":true}}`, string(got.Definition.UI))
	require.Equal(t, "update", got.Definition.Match.Method)
	require.Equal(t, "turn", got.Definition.Activation.Scope)
	require.NoError(t, os.WriteFile(file, append(body, []byte("title: changed\n")...), 0600))
	got, err = resources.ResolveDefinition[primitive.ToolfeedDefinition](ctx, out.PrimitiveProviders, primitive.ToolfeedKind, "media-plan", func(v primitive.ToolfeedDefinition) string { return v.ID })
	require.Error(t, err, "changed immutable source cannot supply a selected definition")
	require.Nil(t, got)
}
func TestDefaultPublicEmptyAndAuthenticatedComposition(t *testing.T) {
	old := workspace.Root()
	workspace.SetRoot(t.TempDir())
	t.Cleanup(func() { workspace.SetRoot(old) })
	mgr, err := manager.New(nil)
	require.NoError(t, err)
	out := &Runtime{MCPManager: mgr}
	require.NoError(t, NewBuilder().configurePrimitiveProviders(out))
	require.NotNil(t, out.PrimitiveProviders)
	// Bootstrapped standard host documents may have a metadata-only provider.
	// Empty composition still declares no business primitive capabilities.
	namespaces, err := out.PrimitiveProviders.Discover(context.Background())
	require.NoError(t, err)
	require.Empty(t, namespaces)
	page, err := resources.ListPage[primitive.ToolfeedDefinition](context.Background(), out.PrimitiveProviders, primitive.ToolfeedKind, resources.CatalogQuery{}, true)
	require.NoError(t, err)
	require.Empty(t, page.Items)
	protectedManager, err := manager.New(nil)
	require.NoError(t, err)
	protected := &Runtime{MCPManager: protectedManager, AuthConfig: &auth.Config{Enabled: true}}
	require.NoError(t, NewBuilder().configurePrimitiveProviders(protected))
	require.NotNil(t, protected.PrimitiveProviders)
	_, err = resources.ListPage[primitive.ToolfeedDefinition](context.Background(), protected.PrimitiveProviders, primitive.ToolfeedKind, resources.CatalogQuery{}, true)
	require.Error(t, err, "normal auth-enabled ingress still requires signed-in user")
	page, err = resources.ListPage[primitive.ToolfeedDefinition](auth.InjectUser(context.Background(), "normal-workspace-user"), protected.PrimitiveProviders, primitive.ToolfeedKind, resources.CatalogQuery{}, true)
	require.NoError(t, err)
	require.Empty(t, page.Items)
}

func TestDefaultPublicFeedAuthenticatedNormalPrincipal(t *testing.T) {
	root := t.TempDir()
	old := workspace.Root()
	workspace.SetRoot(root)
	t.Cleanup(func() { workspace.SetRoot(old) })
	require.NoError(t, os.MkdirAll(filepath.Join(root, "feeds"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "feeds/media.yaml"), []byte("id: media-plan\nmatch: {service: synthetic, method: update}\nui: {title: Preserved}\n"), 0600))
	mgr, err := manager.New(nil)
	require.NoError(t, err)
	out := &Runtime{MCPManager: mgr, AuthConfig: &auth.Config{Enabled: true}}
	require.NoError(t, NewBuilder().configurePrimitiveProviders(out))
	defer out.PrimitiveProviders.Close()
	ctx := auth.InjectUser(context.Background(), "normal-workspace-user")
	got, err := resources.ResolveDefinition[primitive.ToolfeedDefinition](ctx, out.PrimitiveProviders, primitive.ToolfeedKind, "media-plan", func(v primitive.ToolfeedDefinition) string { return v.ID })
	require.NoError(t, err)
	require.Equal(t, "media-plan", got.Definition.ID)
	require.JSONEq(t, `{"title":"Preserved"}`, string(got.Definition.UI))
	_, err = resources.ResolveDefinition[primitive.ToolfeedDefinition](context.Background(), out.PrimitiveProviders, primitive.ToolfeedKind, "media-plan", func(v primitive.ToolfeedDefinition) string { return v.ID })
	require.Error(t, err)
}

func TestDefaultPublicDatasourceAndFeedShareNativeGateway(t *testing.T) {
	root := t.TempDir()
	old := workspace.Root()
	workspace.SetRoot(t.TempDir())
	t.Cleanup(func() { workspace.SetRoot(old) })
	require.NoError(t, os.MkdirAll(filepath.Join(root, "feeds"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "feeds/media.yaml"), []byte("id: media-plan\nmatch: {service: synthetic, method: update}\nui: {title: Original}\n"), 0600))
	dir := filepath.Join(root, workspace.KindForgeDataSource, "nested")
	require.NoError(t, os.MkdirAll(dir, 0700))
	file := filepath.Join(dir, "spend.yaml")
	body := []byte("id: authored-spend\nselectors: {data: data}\nbackend: {kind: mcp_tool, service: original-public-provider, method: query}\n")
	require.NoError(t, os.WriteFile(file, body, 0600))
	mgr, err := manager.New(nil)
	require.NoError(t, err)
	out := &Runtime{MCPManager: mgr, Store: fsstore.New(root)}
	builder := NewBuilder()
	require.NoError(t, builder.configurePrimitiveProviders(out))
	defer out.PrimitiveProviders.Close()
	g := out.PrimitiveProviders
	p := out.LocalPrimitiveProvider
	feed, err := resources.ResolveDefinition[primitive.ToolfeedDefinition](context.Background(), g, primitive.ToolfeedKind, "media-plan", func(v primitive.ToolfeedDefinition) string { return v.ID })
	require.NoError(t, err)
	require.Equal(t, "toolfeed://workspace/media", feed.Resource.URI)
	got, err := resources.ResolveDefinition[*ds.DataSource](context.Background(), g, "datasource", "authored-spend", func(v *ds.DataSource) string { return v.ID }, resources.DecodeDatasourceDefinition)
	require.NoError(t, err)
	require.Equal(t, "datasource://workspace/nested/spend", got.Resource.URI)
	require.Equal(t, "authored-spend", got.Definition.ID)
	require.Equal(t, ds.BackendMCPTool, got.Definition.Backend.Kind)
	require.Equal(t, "original-public-provider", got.Definition.Backend.Service)
	require.Equal(t, "query", got.Definition.Backend.Method)
	require.Equal(t, "data", got.Definition.Selectors.Data)
	require.NoError(t, builder.configurePrimitiveProviders(out))
	require.Same(t, g, out.PrimitiveProviders)
	require.Same(t, p, out.LocalPrimitiveProvider)
	current, err := os.ReadFile(file)
	require.NoError(t, err)
	require.Equal(t, body, current, "authored YAML stays exact")
}
