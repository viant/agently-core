package resource

import (
	"context"
	identity "github.com/viant/forge/backend/resource"
	"github.com/viant/forge/backend/types"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLookupOriginalWorkspaceDefinitionsRemainCompatible(t *testing.T) {
	root := os.Getenv("AGENTLY_STEWARD_FIXTURE_ROOT")
	if root == "" {
		t.Skip("explicit read-only original workspace fixture not supplied")
	}
	definitions, err := SnapshotExtensionDefinitions(context.Background(), root, []ExtensionDirectory{LookupDirectory("workspace", "extension/forge/lookups")})
	require.NoError(t, err)
	require.NotEmpty(t, definitions)
	pickers, err := NativeLookupPickerDefinitions(context.Background(), root, definitions, nil, nil)
	require.NoError(t, err)
	require.NotEmpty(t, pickers)
	definitions = append(definitions, pickers...)
	_, err = NewNativeAssetSnapshot(context.Background(), root, definitions)
	require.NoError(t, err)
}

func TestLookupPickerInventoryAndCompilationResolveNativeImports(t *testing.T) {
	root := t.TempDir()
	write := func(file, body string) {
		target := filepath.Join(root, "extension/forge", file)
		require.NoError(t, os.MkdirAll(filepath.Dir(target), 0700))
		require.NoError(t, os.WriteFile(target, []byte(body), 0600))
	}
	write("lookups/choice.yaml", "id: choice\nbindings:\n - named: {name: choice}\n   lookup: {dialogId: choose, dataSource: rows}\n")
	write("datasources/rows.yaml", "id: rows\ncardinality: collection\nbackend: {kind: inline, rows: []}\n")
	write("dialogs/shared.yaml", "content:\n  id: $param(id)\n  dataSourceRef: rows\n  items: [{id: name, label: Name, type: text}]\n")
	write("dialogs/choose.yaml", "id: choose\ntitle: Choose\ncontent: '$import(shared.yaml:content, {\"id\":\"importedContent\"})'\n")
	// Inventory also encounters dialogs not used by this lookup. They must use
	// the same import semantics rather than attempting to decode a raw string.
	write("dialogs/unrelated.yaml", "id: unrelated\ncontent: '$import(shared.yaml:content, {\"id\":\"otherContent\"})'\n")
	ctx := context.Background()
	definitions, err := SnapshotExtensionDefinitions(ctx, root, []ExtensionDirectory{LookupDirectory("workspace", "extension/forge/lookups")})
	require.NoError(t, err)
	pickers, err := NativeLookupPickerDefinitions(ctx, root, definitions, nil, nil)
	require.NoError(t, err)
	require.Len(t, pickers, 1)
	handle, err := os.OpenRoot(root)
	require.NoError(t, err)
	defer handle.Close()
	reader := NewExtensionReader(handle)
	defer reader.Close()
	raw, err := pickers[0].Load(ctx, reader, pickers[0].File)
	require.NoError(t, err)
	bundle, err := types.SelectWindowResource(raw, nil)
	require.NoError(t, err)
	require.Equal(t, "importedContent", bundle.Window.Dialogs[0].Content.ID)
	require.NotEmpty(t, bundle.DataSources["rows"])
	// Imported source bytes remain part of the same confined dependency guard.
	write("dialogs/choose.yaml", "id: choose\ncontent: $import(../../../../outside.yaml)\n")
	_, err = NativeLookupPickerDefinitions(ctx, root, definitions, nil, nil)
	require.ErrorIs(t, err, identity.ErrResourceDenied)
}
