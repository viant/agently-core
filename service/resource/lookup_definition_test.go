package resource

import (
	"context"
	"os"
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
