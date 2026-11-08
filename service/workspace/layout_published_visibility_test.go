package workspace

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/service/ui/layoutgate"
)

func TestUnresolvedPublishedVisibilityHidesOnlyAffectedWindow(t *testing.T) {
	items := []LayoutMenu{
		{ID: "ordinary", Action: &LayoutAction{Type: "window", providerOwned: true}},
		{ID: "restricted", VisibleWhen: map[string]any{"source": "authorization", "field": "principal.features", "contains": "example"}, Action: &LayoutAction{Type: "window", providerOwned: true}},
	}
	visible, err := filterMenusContextWithRuntime(context.Background(), items, nil, false, false, nil, false, nil)
	require.NoError(t, err)
	require.Len(t, visible, 1)
	require.Equal(t, "ordinary", visible[0].ID)
	items[1].Action.providerOwned = false
	_, err = filterMenusContextWithRuntime(context.Background(), items, nil, false, false, nil, false, nil)
	require.ErrorIs(t, err, layoutgate.ErrFactsUnavailable)
}
