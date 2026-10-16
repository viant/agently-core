package executor

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	uihttp "github.com/viant/agently-core/adapter/http/ui"
	"github.com/viant/agently-core/protocol/mcp/manager"
	forge "github.com/viant/agently-core/service/primitiveprovider"
	resources "github.com/viant/agently-core/service/resource"
	"github.com/viant/agently-core/workspace"
)

func TestMissingWindowImportDoesNotPreventRuntimeStartup(t *testing.T) {
	root := t.TempDir()
	previous := workspace.Root()
	workspace.SetRoot(root)
	defer workspace.SetRoot(previous)
	directory := filepath.Join(root, "extension/forge/windows")
	require.NoError(t, os.MkdirAll(directory, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(directory, "healthy.yaml"), []byte("id: healthy\nwindowKey: healthy\ntitle: Healthy\nview: {content: {id: healthy, title: Healthy}}\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(directory, "broken.yaml"), []byte("windowKey: broken\nview:\n  content: $import(mutation-sources.yaml)\n"), 0600))
	mgr, err := manager.New(nil)
	require.NoError(t, err)
	runtime := &Runtime{MCPManager: mgr, UIBridge: forge.NewService(&forge.Config{})}
	builder := NewBuilder()
	require.NoError(t, builder.configurePrimitiveProviders(runtime), "a missing window fragment must not prevent runtime initialization")
	defer runtime.PrimitiveProviders.Close()
	builder.configureWindowUIProjection(runtime)
	_, err = resources.List[json.RawMessage](context.Background(), runtime.PrimitiveProviders, "window", "workspace")
	require.NoError(t, err, "one broken window must not poison window discovery")
	handler := uihttp.NewEmbeddedHandlerWithAuthorization(root, nil, nil, nil, uihttp.WithWindowResourceProvider(runtime.UIBridge))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", "/window/healthy?platform=web", nil))
	require.Equal(t, 200, response.Code, response.Body.String())
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", "/window/broken?platform=web", nil))
	require.NotEqual(t, 200, response.Code, "broken window must not be served as valid")
}
