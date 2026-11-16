package window

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/workspace"
	meta "github.com/viant/forge/backend/service/meta"
)

// Exercise the shared window loader on independently authored fixtures for each target.
func TestWorkspaceTargetsRespectEachWindowsAssetAssignment(t *testing.T) {
	withLoaderWorkspaceRoot(t, func(root string) {
		for _, id := range []string{"entries", "notes"} {
			mustWriteLoaderFile(t, filepath.Join(root, workspace.KindForgeDataSource, id+".yaml"), "id: "+id+"\ncardinality: collection\nbackend: {kind: inline, rows: []}\n")
			mustWriteLoaderFile(t, filepath.Join(root, workspace.KindForgeDialog, id+".yaml"), "id: "+id+"\ntitle: Example chooser\ncontent: {id: list, dataSourceRef: "+id+"}\n")
			mustWriteLoaderFile(t, filepath.Join(root, workspace.KindForgeWindow, id+".yaml"), "windowKey: "+id+"\nresources:\n  dataSources: ["+id+"]\n  dialogs: ["+id+"]\nview: {content: {id: root, dataSourceRef: "+id+"}}\n")
		}
		mustWriteLoaderFile(t, filepath.Join(root, workspace.KindForgeWindow, "overview.yaml"), "windowKey: overview\nview: {content: {id: overview}}\n")
		for name, target := range map[string]*meta.TargetContext{
			"default": nil, "web": {Platform: "web", Surface: "browser"}, "phone": {Platform: "ios", FormFactor: "phone", Surface: "app"},
			"tablet": {Platform: "ios", FormFactor: "tablet", Surface: "app"}, "android": {Platform: "android", FormFactor: "phone", Surface: "app"},
		} {
			t.Run(name, func(t *testing.T) {
				overview, err := LoadWorkspaceWindow(context.Background(), "overview", target)
				require.NoError(t, err)
				require.NotNil(t, overview)
				require.Empty(t, overview.DataSource)
				require.Empty(t, overview.Dialogs)
				require.Empty(t, overview.Schemas)
				require.Empty(t, overview.ResourceModels)
				for _, id := range []string{"entries", "notes"} {
					got, err := LoadWorkspaceWindow(context.Background(), id, target)
					require.NoError(t, err)
					require.NotNil(t, got)
					require.Equal(t, []string{id}, sortedDataSourceKeys(got))
					require.Len(t, got.Dialogs, 1)
					require.Equal(t, id, got.Dialogs[0].Id)
				}
			})
		}
	})
}
