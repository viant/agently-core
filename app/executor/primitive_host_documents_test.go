package executor

import (
	"context"
	"fmt"
	"github.com/stretchr/testify/require"
	agent "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/binding"
	ds "github.com/viant/agently-core/protocol/datasource"
	"github.com/viant/agently-core/protocol/mcp/manager"
	identity "github.com/viant/agently-core/protocol/resource"
	auth "github.com/viant/agently-core/service/auth"
	"github.com/viant/agently-core/service/planner"
	resources "github.com/viant/agently-core/service/resource"
	"github.com/viant/agently-core/workspace"
	fsstore "github.com/viant/agently-core/workspace/store/fs"
	"github.com/viant/forge/backend/types"
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultHostDocumentsUseSameProviderAndTypedReaders(t *testing.T) {
	root := t.TempDir()
	old := workspace.Root()
	workspace.SetRoot(root)
	t.Cleanup(func() { workspace.SetRoot(old) })
	files := map[string]string{"tools/bundles/basic.yaml": "id: basic\ndescription: Original bundle\nmatch: [{name: 'system/os:getEnv'}]\n", "tools/instructions/webdriver.md": "Original webdriver instructions.", "intake/root.tmpl": "Original sidecar\n$import(parts/detail.md)\n", "intake/parts/detail.md": "Imported original detail.", "agents/planner/planner.schema.json": "{\"type\":\"object\",\"properties\":{\"tasks\":{\"type\":\"array\"}}}", "agents/planner/planner.validation.json": "{\"rules\":[]}", "playbooks/review.md": "---\nkind: playbook\nid: review\n---\n---\nrole: system\n---\n# Instructions\nUse evidence.", "intents/scenarios/partial.user.md": "support fragment, not a playbook root"}
	for name, raw := range files {
		file := filepath.Join(root, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(file), 0700))
		require.NoError(t, os.WriteFile(file, []byte(raw), 0600))
	}
	mgr, err := manager.New(nil)
	require.NoError(t, err)
	t.Cleanup(func() { mgr.CloseConversation("") })
	out := &Runtime{MCPManager: mgr, Store: fsstore.New(root)}
	builder := NewBuilder()
	require.NoError(t, builder.configurePrimitiveProviders(out))
	t.Cleanup(out.PrimitiveProviders.Close)
	pointer := out.PrimitiveProviders
	provider := out.LocalPrimitiveProvider
	require.NoError(t, builder.configurePrimitiveProviders(out))
	require.Same(t, pointer, out.PrimitiveProviders)
	require.Same(t, provider, out.LocalPrimitiveProvider)
	playbooks, err := resources.NewPlaybookReader(out.PrimitiveProviders).LoadAll(context.Background())
	require.NoError(t, err)
	require.Len(t, playbooks, 1)
	require.Equal(t, "playbook://workspace/review", playbooks[0].ResourceURI)
	selected, err := resources.NewPlaybookReader(out.PrimitiveProviders).Load(context.Background(), "playbook://workspace/review")
	require.NoError(t, err)
	require.Equal(t, "# Instructions\nUse evidence.", selected.Messages[0].Text)
	legacy, err := resources.NewIntentReader(out.PrimitiveProviders).LoadAll(context.Background())
	require.NoError(t, err)
	require.Empty(t, legacy, "supporting Markdown fragments cannot become legacy intent roots")
	reader := out.NativeHostDocuments
	require.NotNil(t, reader)
	bundles, err := reader.LoadAll(context.Background())
	require.NoError(t, err)
	found := false
	for _, bundle := range bundles {
		if bundle.ID == "basic" {
			found = true
		}
	}
	require.True(t, found)
	raw, err := reader.ReadURI(context.Background(), "file://"+filepath.Join(root, "intake/root.tmpl"))
	require.NoError(t, err)
	expanded, err := binding.ResolveTextImportsWithReader(context.Background(), reader.ReadURI, string(raw), "file://"+filepath.Join(root, "intake/root.tmpl"))
	require.NoError(t, err)
	require.Contains(t, expanded, "Imported original detail.")
	resolver := planner.NewFileResolver(nil)
	resolver.SetDocumentReader(reader)
	contract, err := resolver.Resolve(context.Background(), &agent.Agent{Source: &agent.Source{URL: "file://" + filepath.Join(root, "agents/planner/agent.yaml")}})
	require.NoError(t, err)
	schema, err := contract.Schema(context.Background())
	require.NoError(t, err)
	require.Equal(t, "object", schema["type"])
	_, err = reader.ReadURI(context.Background(), "file://"+filepath.Join(root, "agents/planner/not-registered.json"))
	require.Error(t, err)
}

func TestPinnedDatasourceHostWithNativeDocumentsPreservesBoundary(t *testing.T) {
	root := t.TempDir()
	prior := workspace.Root()
	workspace.SetRoot(root)
	t.Cleanup(func() { workspace.SetRoot(prior) })
	require.NoError(t, os.MkdirAll(filepath.Join(root, "intake"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "intake/original.md"), []byte("Original native host document."), 0600))
	for _, authenticated := range []bool{false, true} {
		t.Run(fmt.Sprintf("auth=%t", authenticated), func(t *testing.T) {
			calls := 0
			revalidate := func(_ context.Context, pin identity.ResolvedResource) (*identity.ResolvedResource, error) {
				calls++
				return &pin, nil
			}
			resolve := func(_ context.Context, _ identity.ResolvedResource, _ *types.WindowTarget, id string) (*ds.DataSource, error) {
				calls++
				return &ds.DataSource{ID: id}, nil
			}
			authorize := func(context.Context, *ds.DataSource, map[string]interface{}) error {
				calls++
				return identity.ErrResourceDenied
			}
			mgr, err := manager.New(nil)
			require.NoError(t, err)
			t.Cleanup(func() { mgr.CloseConversation("") })
			out := &Runtime{MCPManager: mgr, Store: fsstore.New(root), AuthConfig: &auth.Config{Enabled: authenticated}, DatasourceResourceRevalidator: revalidate, DatasourceDefinitionResolver: resolve, DatasourceDefinitionAuthorizer: authorize}
			builder := NewBuilder().WithPinnedDatasourceDefinitions(revalidate, resolve, authorize)
			require.NoError(t, builder.configurePrimitiveProviders(out))
			require.Zero(t, calls, "pinned datasource authority must not run at startup")
			require.NotNil(t, out.PrimitiveProviders)
			t.Cleanup(out.PrimitiveProviders.Close)
			gateway := out.PrimitiveProviders
			require.NoError(t, builder.configurePrimitiveProviders(out))
			require.Same(t, gateway, out.PrimitiveProviders)
			ctx := context.Background()
			if authenticated {
				_, err = out.NativeHostDocuments.Read(ctx, "intake/original.md")
				require.Error(t, err)
				ctx = auth.InjectUser(ctx, "current-user")
			}
			raw, err := out.NativeHostDocuments.Read(ctx, "intake/original.md")
			require.NoError(t, err)
			require.Equal(t, "Original native host document.", string(raw))
			require.Zero(t, calls)
			pin := identity.ResolvedResource{URI: "datasource://original/records"}
			got, err := out.DatasourceResourceRevalidator(ctx, pin)
			require.NoError(t, err)
			require.Equal(t, pin, *got)
			definition, err := out.DatasourceDefinitionResolver(ctx, pin, nil, "records")
			require.NoError(t, err)
			require.Equal(t, "records", definition.ID)
			require.ErrorIs(t, out.DatasourceDefinitionAuthorizer(ctx, definition, nil), identity.ErrResourceDenied)
			require.Equal(t, 3, calls)
		})
	}
}
