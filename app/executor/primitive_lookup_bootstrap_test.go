package executor

import (
	"context"
	"encoding/json"
	"github.com/viant/agently-core/internal/testutil/catalogfixture"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/forge/backend/types"
	"testing"

	"github.com/stretchr/testify/require"
	lookup "github.com/viant/agently-core/protocol/lookup/overlay"
	"github.com/viant/agently-core/protocol/mcp/manager"
	primitive "github.com/viant/agently-core/protocol/primitive"
	resources "github.com/viant/agently-core/service/resource"
	"github.com/viant/agently-core/workspace"
)

func TestLookupBootstrapPublishesAuthoredOverlayThroughNativeMCP(t *testing.T) {
	root := catalogfixture.Lookup(t)
	previous := workspace.Root()
	workspace.SetRoot(root)
	t.Cleanup(func() { workspace.SetRoot(previous) })
	mgr, err := manager.New(nil)
	require.NoError(t, err)
	runtime := &Runtime{MCPManager: mgr}
	require.NoError(t, NewBuilder().configurePrimitiveProviders(runtime))
	defer runtime.PrimitiveProviders.Close()
	page, err := resources.ListPage[*lookup.Overlay](context.Background(), runtime.PrimitiveProviders, "lookup", resources.CatalogQuery{Namespace: "workspace", Limit: 100}, true)
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	require.Equal(t, "lookup://workspace/document_lookup", page.Items[0].Resource.URI)
	definition, err := resources.DecodeLookupDefinition(1, page.Items[0].Resource.Definition)
	require.NoError(t, err)
	require.Equal(t, "document", definition.Bindings[0].Named.Name)
	require.Equal(t, "document_lookup", definition.Bindings[0].Lookup.DataSource)
	require.Equal(t, "chooseDocument", definition.Bindings[0].Lookup.DialogId)
	picker, err := resources.Get[json.RawMessage](context.Background(), runtime.PrimitiveProviders, page.Items[0].Connection, identity.ResourceRef{URI: "window://workspace/chooseDocument"}, nil)
	require.NoError(t, err)
	variant, err := types.SelectWindowResource(picker.Definition, nil)
	require.NoError(t, err)
	_, err = types.SelectWindowResource(picker.Definition, &types.WindowTarget{Platform: "web", FormFactor: "desktop", Surface: "browser", Capabilities: []string{"markdown", "chart", "upload", "code", "diff"}})
	require.NoError(t, err, "the lookup picker must support the regular web workspace target")
	require.Len(t, variant.Window.Dialogs, 1)
	dialog := variant.Window.Dialogs[0]
	require.Equal(t, "chooseDocument", dialog.Id)
	require.Equal(t, "single", dialog.SelectionMode)
	require.Equal(t, "Key", dialog.Properties.ResolveInput)
	require.Len(t, dialog.Properties.QuickFilters, 2)
	require.Equal(t, "example-picker", dialog.ClassName)
	require.Contains(t, variant.DataSources, "document_lookup")
	caps, err := runtime.LocalPrimitiveProvider.NamespaceGet(context.Background(), primitive.NamespaceGetRequest{Namespace: "workspace"})
	require.NoError(t, err)
	found := false
	for _, kind := range caps.Kinds {
		if kind.Kind == "lookup" {
			found = true
			require.Equal(t, primitive.Methods("lookup", []string{"list", "get"}), kind.Methods)
		}
	}
	require.True(t, found)
}
