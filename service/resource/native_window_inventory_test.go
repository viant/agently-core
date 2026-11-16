package resource

import (
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/internal/testutil/catalogfixture"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/forge/backend/types"
	"testing"
)

func TestFixtureDefaultWindowInventoryCompilesOwningBundles(t *testing.T) {
	root := catalogfixture.New(t)
	bindings, err := NativeWorkspaceWindowBindings(context.Background(), root, "workspace")
	require.NoError(t, err)
	require.NotEmpty(t, bindings)
	var found bool
	for _, b := range bindings {
		if b.WindowKey == "documents" {
			found = true
		}
		require.NotContains(t, b.URI, "/shared/")
		require.NotContains(t, b.URI, "/mobile/")
	}
	require.True(t, found)
	defs, err := SnapshotWindowDefinitions(context.Background(), root, bindings, nil)
	require.NoError(t, err)
	snapshot, err := NewNativeAssetSnapshot(context.Background(), root, defs, NativeSnapshotOptions{})
	require.NoError(t, err)
	require.EqualValues(t, len(defs), snapshot.CompileCount())
	for key, title := range map[string]string{"documents": "Documents", "collections": "Collections"} {
		uri, err := identity.ParseResourceURI("window://workspace/" + key)
		require.NoError(t, err)
		candidates, err := snapshot.Candidates(context.Background(), uri)
		require.NoError(t, err)
		require.Len(t, candidates, 1)
		raw, err := snapshot.ReadCandidate(context.Background(), uri, candidates[0])
		require.NoError(t, err)
		var envelope types.WindowResourceEnvelope
		require.NoError(t, json.Unmarshal(raw, &envelope))
		for variantID, variant := range envelope.Variants {
			require.Equal(t, title, variant.Window.Title, "%s variant %s", key, variantID)
		}
	}
	require.EqualValues(t, len(defs), snapshot.CompileCount(), "warm reads must not recompile")
}
