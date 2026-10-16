package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	identity "github.com/viant/agently-core/protocol/resource"
	bridge "github.com/viant/agently-core/service/primitiveprovider"
	resources "github.com/viant/agently-core/service/resource"
	windowloader "github.com/viant/agently-core/service/ui/window"
	"github.com/viant/agently-core/workspace"
)

type scopedLayoutFixture struct {
	global, scoped int
	failGlobal     bool
}

func (*scopedLayoutFixture) UsesWindowResourceResolution() bool      { return true }
func (*scopedLayoutFixture) MetadataScope() bridge.MetadataReadScope { return nil }
func (f *scopedLayoutFixture) WindowDefinitionsList(context.Context, *bridge.WindowDefinitionListInput) (*bridge.WindowDefinitionListOutput, error) {
	f.global++
	if f.failGlobal {
		return nil, errors.New("unrelated global discovery")
	}
	return &bridge.WindowDefinitionListOutput{Windows: []bridge.WindowDefinitionSummary{{WindowID: "provider:operations:remote", ResourceURI: "window://operations/remote"}}}, nil
}
func (f *scopedLayoutFixture) ListFrom(_ context.Context, roots []resources.Connection, _ *bridge.WindowDefinitionListInput) (*bridge.WindowDefinitionListOutput, error) {
	f.scoped++
	if len(roots) != 1 || roots[0].Name != "internal" || roots[0].ProviderIdentity != "internal" {
		return nil, identity.ErrResourceDenied
	}
	return &bridge.WindowDefinitionListOutput{Windows: []bridge.WindowDefinitionSummary{{WindowID: "home", ResourceURI: "window://workspace/home"}}}, nil
}
func TestENGScopedLayoutInventoryKeepsRawAndExternalFallback(t *testing.T) {
	previous := workspace.Root()
	workspace.SetRoot(t.TempDir())
	t.Cleanup(func() { workspace.SetRoot(previous) })
	for _, test := range []struct {
		name              string
		managed, external bool
	}{{"managed internal", true, false}, {"managed external", true, true}, {"raw external", false, true}} {
		t.Run(test.name, func(t *testing.T) {
			action := &LayoutAction{Type: "window", WindowKey: "home"}
			if test.external {
				action.Provider = "operations"
				action.WindowKey = "remote"
			}
			layout := Layout{Version: 1, ID: "test", Applications: []LayoutApplication{{ID: "workspace", Title: "Workspace", Menus: []LayoutMenu{{ID: "action", Title: "Action", Action: action}}}}}
			if test.external {
				layout.WindowProviders = []windowloader.RemoteProvider{{ID: "operations", Type: "mcp", ServerRef: "operations", CatalogTool: "windows/list", WindowTool: "windows/get"}}
			}
			raw, err := json.Marshal(layout)
			require.NoError(t, err)
			handler := NewMetadataHandler(nil, nil, "test")
			require.NoError(t, handler.SetCapturedLayout(workspace.Root(), "embedded", raw, func(context.Context) error { return nil }))
			source := &scopedLayoutFixture{failGlobal: !test.external}
			handler.SetWindowResourceCatalog(ScopedLayoutWindowCatalog(source, source, []resources.Connection{{Name: "internal", ProviderIdentity: "internal"}}))
			if test.managed {
				applications, err := json.Marshal(layout.Applications)
				require.NoError(t, err)
				handler.SetLayoutMenuReader(func(context.Context) (*LayoutMenuRead, error) {
					return &LayoutMenuRead{Applications: applications, Revision: "original", Revalidate: func(context.Context) error { return nil }}, nil
				})
			}
			result, err := handler.Layout(context.Background(), nil)
			require.NoError(t, err)
			require.Len(t, result.Layout.Applications, 1)
			require.Len(t, result.Layout.Applications[0].Menus, 1)
			if test.external {
				require.Equal(t, 0, source.scoped)
				require.Positive(t, source.global)
				require.Equal(t, "operations", result.Layout.Applications[0].Menus[0].Action.Provider)
			} else {
				require.Equal(t, 0, source.global)
				require.Positive(t, source.scoped)
			}
		})
	}
}
