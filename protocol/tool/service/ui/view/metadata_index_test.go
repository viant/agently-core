package view

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/afs"
	identity "github.com/viant/agently-core/protocol/resource"
	forge "github.com/viant/agently-core/service/primitiveprovider"
	repo "github.com/viant/agently-core/workspace/repository/forgewindow"
	"github.com/viant/forge/backend/types"
)

type indexedViewCatalog struct {
	gets, resolves int
	denyList       bool
}

func (*indexedViewCatalog) AuthzReady() bool             { return true }
func (*indexedViewCatalog) UsesResourceResolution() bool { return true }
func (*indexedViewCatalog) MetadataOnlyWindowList() bool { return true }
func (*indexedViewCatalog) ConfiguredWindowIDs() []string {
	keys := make([]string, 20)
	for i := range keys {
		keys[i] = fmt.Sprintf("window%d", i)
	}
	return keys
}
func (*indexedViewCatalog) ResourceReference(_ context.Context, key string) (identity.ResourceRef, error) {
	return identity.ResourceRef{URI: "window://platform/" + key}, nil
}
func (c *indexedViewCatalog) List(context.Context, *forge.WindowDefinitionListInput) (*forge.WindowDefinitionListOutput, error) {
	if c.denyList {
		return nil, identity.ErrResourceDenied
	}
	out := &forge.WindowDefinitionListOutput{}
	for _, key := range c.ConfiguredWindowIDs() {
		out.Windows = append(out.Windows, forge.WindowDefinitionSummary{WindowID: key, ResourceURI: "window://platform/" + key, ProviderIdentity: "internal", Namespace: "platform"})
	}
	return out, nil
}
func (c *indexedViewCatalog) Get(context.Context, *forge.WindowDefinitionGetInput) (*forge.WindowDefinitionGetOutput, error) {
	c.gets++
	c.resolves++
	return nil, identity.ErrResourceDenied
}

func TestViewNativeMetadataIndexListsWithoutGetOrOpenAuthority(t *testing.T) {
	withWorkspaceRoot(t, func(root string) {
		for i := 0; i < 20; i++ {
			key := fmt.Sprintf("window%d", i)
			mustWriteFile(t, root+"/extension/forge/windows/"+key+".yaml", "id: "+key+"\ntitle: "+key+"\nwindowKey: "+key+"\n")
		}
		catalog := &indexedViewCatalog{}
		bootstrap := 0
		bridge := forge.NewService(&forge.Config{WindowDefinitions: catalog, WindowOpenAdmission: func(context.Context, identity.ResolvedResource, *types.Window, map[string]any) (*forge.WindowOpenDecision, error) {
			bootstrap++
			return nil, identity.ErrResourceDenied
		}})
		svc := New(repo.New(afs.New()), bridge)
		list, err := svc.Method("list")
		require.NoError(t, err)
		out := &ListOutput{}
		require.NoError(t, list(context.Background(), &ListInput{}, out))
		require.Len(t, out.Items, 20)
		for _, item := range out.Items {
			require.Equal(t, "internal", item.ProviderIdentity)
			require.Equal(t, "window://platform/"+item.ID, item.ResourceURI)
			require.Nil(t, item.Resource)
			require.Nil(t, item.Target)
		}
		require.Zero(t, catalog.gets)
		require.Zero(t, catalog.resolves)
		require.Zero(t, bootstrap)
		get, err := svc.Method("get")
		require.NoError(t, err)
		got := &GetOutput{}
		require.ErrorIs(t, get(context.Background(), &GetInput{ID: "window0"}, got), identity.ErrResourceDenied)
		require.Nil(t, got.Item)
		require.Equal(t, 1, catalog.gets)
		_, err = svc.openResolvedItem(context.Background(), "client", "namespace", "conversation", OpenItem{ID: "window0", Parameters: map[string]any{"resourceData": map[string]any{"read": true}}}, 1)
		require.ErrorIs(t, err, identity.ErrResourceDenied)
		require.Equal(t, 2, catalog.gets)
		require.Zero(t, bootstrap, "denied exact definition cannot reach protected bootstrap")
		catalog.denyList = true
		require.Error(t, list(context.Background(), &ListInput{}, out))
		require.Nil(t, out.Items, "revoked namespace/group visibility must discard list")
	})
}
