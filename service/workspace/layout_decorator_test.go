package workspace

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	ws "github.com/viant/agently-core/workspace"
)

func TestLayoutDecoratorIsOptionalAndRevisionTracksHostActions(t *testing.T) {
	previous := ws.Root()
	ws.SetRoot(t.TempDir())
	t.Cleanup(func() { ws.SetRoot(previous) })
	h := NewMetadataHandler(nil, nil, "")
	h.SetAuthorizationRuntimes(nil, nil)
	h.SetLayoutDefault([]byte("version: 1\nid: example\ntopbar:\n  actions:\n    - {id: tasks, title: Tasks, action: {type: window, windowKey: schedule}}\napplications: []\n"))
	ctx := context.Background()
	before, err := h.Layout(ctx, &LayoutRequest{})
	require.NoError(t, err)
	h.SetLayoutDecorator(func(_ context.Context, layout *Layout) error {
		layout.Topbar.Actions = append(layout.Topbar.Actions, LayoutMenu{ID: "host-control", Title: "Host control", Action: &LayoutAction{Type: "window", WindowKey: "host-control"}})
		return nil
	})
	decorated, err := h.Layout(ctx, &LayoutRequest{})
	require.NoError(t, err)
	require.Len(t, decorated.Layout.Topbar.Actions, 2)
	require.Equal(t, before.Layout.Topbar.Actions[0], decorated.Layout.Topbar.Actions[0])
	require.NotEqual(t, before.LayoutRevision, decorated.LayoutRevision)
	again, err := h.Layout(ctx, &LayoutRequest{})
	require.NoError(t, err)
	require.Equal(t, decorated.LayoutRevision, again.LayoutRevision)
	h.SetLayoutDecorator(nil)
	restored, err := h.Layout(ctx, &LayoutRequest{})
	require.NoError(t, err)
	require.Equal(t, before, restored)
}
