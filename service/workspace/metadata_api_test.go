package workspace

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTypedMetadataAndLayoutMatchLegacyTransport(t *testing.T) {
	handler := NewMetadataHandler(nil, nil, "typed-version")
	typed, err := handler.Metadata(context.Background())
	require.NoError(t, err)
	response := httptest.NewRecorder()
	handler.handleMetadata()(response, httptest.NewRequest("GET", "/v1/workspace/metadata", nil))
	require.Equal(t, 200, response.Code)
	raw, err := json.Marshal(typed)
	require.NoError(t, err)
	require.JSONEq(t, string(raw), response.Body.String())
	handler.SetLayoutDefault([]byte("version: 1\nid: typed\napplications: []\n"))
	layout, err := handler.Layout(context.Background(), &LayoutRequest{})
	require.NoError(t, err)
	response = httptest.NewRecorder()
	handler.handleLayout()(response, httptest.NewRequest("GET", "/v1/workspace/layout", nil))
	require.Equal(t, 200, response.Code)
	require.Equal(t, "private, no-store", response.Header().Get("Cache-Control"))
	raw, err = json.Marshal(layout)
	require.NoError(t, err)
	require.JSONEq(t, string(raw), response.Body.String())
}
