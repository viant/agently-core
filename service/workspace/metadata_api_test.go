package workspace

import (
	"context"
	"encoding/json"
	"errors"
	iauth "github.com/viant/agently-core/internal/auth"
	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
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

func TestOptionalBrowserConfigurationFailureKeepsWorkspaceAvailable(t *testing.T) {
	handler := NewMetadataHandler(nil, nil, "example-version")
	handler.SetBrowserMCPLoader(func(context.Context) ([]mcpcfg.BrowserDescriptor, error) {
		return []mcpcfg.BrowserDescriptor{{Name: "partial"}}, errors.New("private configuration details")
	})
	ctx := iauth.WithUserInfo(context.Background(), &iauth.UserInfo{Subject: "example-user"})
	metadata, err := handler.Metadata(ctx)
	require.NoError(t, err)
	require.Empty(t, metadata.BrowserMCP)
	require.Equal(t, []string{"Browser tools are temporarily unavailable."}, metadata.BrowserMCPDiagnostics)
	require.Equal(t, "example-version", metadata.Version)
	require.True(t, metadata.Capabilities.WorkspaceLayout)
	response := httptest.NewRecorder()
	handler.handleMetadata()(response, httptest.NewRequest("GET", "/v1/workspace/metadata", nil).WithContext(ctx))
	require.Equal(t, 200, response.Code)
	require.NotContains(t, response.Body.String(), "private configuration details")
	handler.SetBrowserMCPLoader(func(context.Context) ([]mcpcfg.BrowserDescriptor, error) {
		return []mcpcfg.BrowserDescriptor{{Name: "recovered"}}, nil
	})
	recovered, err := handler.Metadata(ctx)
	require.NoError(t, err)
	require.Empty(t, recovered.BrowserMCPDiagnostics)
	require.Len(t, recovered.BrowserMCP, 1)
	require.NotEqual(t, metadata.MetadataVersion, recovered.MetadataVersion)
}
