package workspace

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	ws "github.com/viant/agently-core/workspace"
)

func TestPublishedApplicationScopeSurvivesLayoutResponse(t *testing.T) {
	previous := ws.Root()
	ws.SetRoot(t.TempDir())
	defer ws.SetRoot(previous)
	handler := NewMetadataHandler(nil, nil, "test")
	handler.SetAuthorizationRuntimes(nil, nil)
	handler.SetLayoutDefault([]byte(menuShell))
	handler.SetLayoutMenuReader(func(context.Context) (*LayoutMenuRead, error) {
		return &LayoutMenuRead{Applications: json.RawMessage(`[{"id":"example","title":"Example","scope":{"keys":["entityId","entityName"]},"menus":[{"id":"choose","title":"Choose entity","scopeLabelKey":"entityName","action":{"type":"window","windowKey":"chooser"}}]}]`), Revision: "scope-one", Revalidate: func(context.Context) error { return nil }}, nil
	})
	result, err := handler.Layout(context.Background(), &LayoutRequest{})
	require.NoError(t, err)
	encoded, err := json.Marshal(result.Layout.Applications)
	require.NoError(t, err)
	var apps []map[string]any
	require.NoError(t, json.Unmarshal(encoded, &apps))
	require.Equal(t, map[string]any{"keys": []any{"entityId", "entityName"}}, apps[0]["scope"])
	menus := apps[0]["menus"].([]any)
	require.Equal(t, "entityName", menus[0].(map[string]any)["scopeLabelKey"])
}
