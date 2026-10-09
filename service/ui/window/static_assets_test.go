package window

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/afs"
	identity "github.com/viant/agently-core/protocol/resource"
)

func TestStaticAssetCompilationHydratesOnceAndClonesAcrossWindowsAndTargets(t *testing.T) {
	root := t.TempDir()
	write := func(name, body string) {
		file := filepath.Join(root, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(file), 0700))
		require.NoError(t, os.WriteFile(file, []byte(body), 0600))
	}
	write("extension/forge/datasources/records.yaml", "id: records\nselectors: {data: data}\nfields:\n - {name: id, type: int}\n")
	write("extension/forge/dialogs/detail.yaml", "id: detail\ncontent: {id: detailRoot, title: Original}\n")
	for _, name := range []string{"first", "second"} {
		write("extension/forge/windows/"+name+".yaml", "view:\n content: {id: "+name+"}\nresources:\n dataSources: [records]\n dialogs: [detail]\n")
	}
	assets := NewStaticAssetCompilation()
	options := LoaderOptions{FS: afs.New(), StaticAssets: assets}
	ctx := context.Background()
	bindings := []ResourceBinding{{WindowKey: "first", URI: "window://team/first"}, {WindowKey: "second", URI: "window://team/second"}}
	source, err := NewWorkspaceResourceSource(root, bindings, nil)
	require.NoError(t, err)
	var original []byte
	for _, binding := range bindings {
		uri, _ := identity.ParseResourceURI(binding.URI)
		got, err := source.LoadBundleWithOptions(ctx, uri, options)
		require.NoError(t, err)
		legacy, err := source.LoadBundleWithOptions(ctx, uri, LoaderOptions{FS: afs.New()})
		require.NoError(t, err)
		require.JSONEq(t, string(legacy), string(got), "static sharing must retain exact original window/target semantics")
		original = got
	}
	require.NotEmpty(t, original)
	require.EqualValues(t, 1, assets.HydrationCount(), "all windows and standard target branches share one authored catalog hydration")
	first, err := LoadWorkspaceWindowWithOptionsAt(ctx, root, "first", nil, nil, options)
	require.NoError(t, err)
	first.Dialogs[0].Content.Title = "Changed only in first window"
	ds := first.DataSource["records"]
	originalSelector := ds.Selectors.Data
	ds.Selectors.Data = "changed"
	first.DataSource["records"] = ds
	second, err := LoadWorkspaceWindowWithOptionsAt(ctx, root, "second", nil, nil, options)
	require.NoError(t, err)
	require.Equal(t, "Original", second.Dialogs[0].Content.Title)
	require.Equal(t, originalSelector, second.DataSource["records"].Selectors.Data)
	require.EqualValues(t, 1, assets.HydrationCount())
}
