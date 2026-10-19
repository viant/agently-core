package workspace

import (
	"encoding/json"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestNavigationPreservesKnownOwnerOnlyWithinItsNamespace(t *testing.T) {
	raw := json.RawMessage(`{"id":"studio","menus":[{"id":"group","children":[{"id":"local","action":{"type":"window","resourceUri":"window://studio/editor","parameters":{"id":9007199254740993}}},{"id":"other","action":{"type":"window","resourceUri":"window://other/view"}}]}]}`)
	bound, err := bindNavigationOwner(raw, "studio", "studio-provider")
	require.NoError(t, err)
	require.Contains(t, string(bound), `"providerIdentity":"studio-provider"`)
	require.Contains(t, string(bound), `9007199254740993`)
	var app struct {
		Menus []struct {
			Children []struct{ Action LayoutAction }
		}
	}
	require.NoError(t, json.Unmarshal(bound, &app))
	require.Equal(t, "studio-provider", app.Menus[0].Children[0].Action.ProviderIdentity)
	require.Empty(t, app.Menus[0].Children[1].Action.ProviderIdentity)
	require.NotContains(t, string(raw), "providerIdentity", "source remains unchanged")
}
