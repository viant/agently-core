package resource

import (
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/forge/backend/types"
	"os"
	"testing"
)

func TestOriginalStewardDefaultWindowInventoryCompilesOwningBundles(t *testing.T) {
	root := os.Getenv("AGENTLY_AUTHORED_FIXTURE_ROOT")
	if root == "" {
		t.Skip("original Steward fixture not configured")
	}
	bindings, err := NativeWorkspaceWindowBindings(context.Background(), root, "workspace")
	require.NoError(t, err)
	require.NotEmpty(t, bindings)
	var found bool
	for _, b := range bindings {
		if b.WindowKey == "advertiserList" {
			found = true
		}
		require.NotContains(t, b.URI, "/shared/")
		require.NotContains(t, b.URI, "/mobile/")
	}
	require.True(t, found)
	defs, err := SnapshotWindowDefinitions(context.Background(), root, bindings, nil)
	require.NoError(t, err)
	snapshot, err := NewNativeAssetSnapshot(context.Background(), root, defs, NativeSnapshotOptions{AssetPaths: []string{"extension/forge"}, ImmutableUntilRestart: true})
	require.NoError(t, err)
	require.EqualValues(t, len(defs), snapshot.CompileCount())
	for key, title := range map[string]string{"advertiserList": "Advertisers", "order": "Order"} {
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
