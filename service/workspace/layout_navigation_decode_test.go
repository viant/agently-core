package workspace

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPortableNavigationAliasesAndDisabledLabels(t *testing.T) {
	for _, key := range []string{"menus", "children", "items"} {
		var layout Layout
		raw := `{"version":1,"id":"workspace","applications":[{"id":"reports","title":"Reports","menus":[{"id":"group","title":"Group","` + key + `":[{"id":"open","title":"Open","action":{"resourceUri":"window://example/report","parameters":{"ViewId":42},"parameterBindings":{"AdvertiserId":{"from":["AdvertiserId","advertiserId"],"array":true}}}},{"id":"unavailable","title":"Unavailable","disabled":true}]}]}]}`
		require.NoError(t, json.Unmarshal([]byte(raw), &layout))
		require.NoError(t, validateLayout(&layout))
		require.Equal(t, "window", layout.Applications[0].Menus[0].Children[0].Action.Type)
		require.EqualValues(t, 42, layout.Applications[0].Menus[0].Children[0].Action.Parameters["ViewId"])
		require.Contains(t, layout.Applications[0].Menus[0].Children[0].Action.ParameterBindings, "AdvertiserId")
		filtered, err := filterLayoutContextWithRuntimes(context.Background(), &layout, nil, nil)
		require.NoError(t, err)
		require.True(t, filtered.Applications[0].Menus[0].Children[1].Disabled)
		require.Equal(t, layout.Applications[0].Menus[0].Children[0].Action.ParameterBindings, filtered.Applications[0].Menus[0].Children[0].Action.ParameterBindings)
	}
}

func TestPortableNavigationRejectsAmbiguousChildCollections(t *testing.T) {
	var menu LayoutMenu
	require.Error(t, json.Unmarshal([]byte(`{"id":"group","title":"Group","menus":[],"children":[]}`), &menu))
}
