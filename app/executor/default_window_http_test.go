package executor

import (
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	uihttp "github.com/viant/agently-core/adapter/http/ui"
	"github.com/viant/agently-core/protocol/mcp/manager"
	identity "github.com/viant/agently-core/protocol/resource"
	uiview "github.com/viant/agently-core/protocol/tool/service/ui/view"
	forge "github.com/viant/agently-core/service/primitiveprovider"
	resources "github.com/viant/agently-core/service/resource"
	"github.com/viant/agently-core/workspace"
	viewrepo "github.com/viant/agently-core/workspace/repository/forgewindow"
	"github.com/viant/forge/backend/types"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultWindowHTTPAndViewConsumeSameMCPDefinition(t *testing.T) {
	root := t.TempDir()
	old := workspace.Root()
	workspace.SetRoot(root)
	defer workspace.SetRoot(old)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "extension/forge/windows"), 0700))
	raw := []byte("id: original\ntitle: Original\nwindowKey: original\nquickSearch: {dataSourceRef: records, field: Name}\nidentityScope: conversation\nopenMode: replace\nparameters: [{name: AdvertiserId, type: 'int[]', required: true}]\nview: {content: {id: original, title: Original}}\n")
	file := filepath.Join(root, "extension/forge/windows/original.yaml")
	require.NoError(t, os.WriteFile(file, raw, 0600))
	mgr, err := manager.New(nil)
	require.NoError(t, err)
	rt := &Runtime{MCPManager: mgr, UIBridge: forge.NewService(&forge.Config{})}
	b := NewBuilder()
	require.NoError(t, b.configurePrimitiveProviders(rt))
	b.configureDefaultWindowOpenAdmission(rt)
	defer rt.PrimitiveProviders.Close()
	require.True(t, rt.UIBridge.UsesWindowResourceResolution())
	startedList := time.Now()
	_, err = resources.List[json.RawMessage](context.Background(), rt.PrimitiveProviders, "window", "workspace")
	require.NoError(t, err)
	t.Logf("cold metadata List %s", time.Since(startedList))
	startedList = time.Now()
	_, err = resources.List[json.RawMessage](context.Background(), rt.PrimitiveProviders, "window", "workspace")
	require.NoError(t, err)
	t.Logf("warm metadata List %s", time.Since(startedList))
	reader := resources.NewWindowViewReader(rt.PrimitiveProviders)
	specs, err := reader.LoadAll(context.Background())
	require.NoError(t, err)
	require.Len(t, specs, 1)
	require.Equal(t, "conversation", specs[0].IdentityScope)
	require.Equal(t, "replace", specs[0].OpenMode)
	require.Equal(t, "Name", specs[0].QuickSearch.Field)
	views := uiview.New(viewrepo.NewWithReader(reader), rt.UIBridge)
	get, err := views.Method("get")
	require.NoError(t, err)
	output := &uiview.GetOutput{}
	require.NoError(t, get(context.Background(), &uiview.GetInput{ID: "original"}, output))
	require.Equal(t, "Original", output.Item.Title)
	require.Equal(t, "window://workspace/original", output.Item.Resource.URI)
	handler := uihttp.NewEmbeddedHandlerWithAuthorization(root, nil, nil, nil, uihttp.WithWindowResourceProvider(rt.UIBridge))
	started := time.Now()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", "/window/original?applyPermission=true&platform=web", nil))
	require.Equal(t, 200, response.Code, response.Body.String())
	t.Logf("warm actual HTTP window GET %s", time.Since(started))
	var envelope struct {
		Data types.Window `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
	window := envelope.Data
	require.Equal(t, "Original", window.Title)
	require.Equal(t, "window://workspace/original", window.Resource.URI)
	require.NotEmpty(t, window.ResourceTarget.SelectionToken)
	expired := *window.Resource
	expired.ValidUntil = time.Now().Add(-time.Second)
	renewed, err := rt.PrimitiveWindows.RenewDefaultPresentation(context.Background(), "original", expired, window.ResourceTarget)
	require.NoError(t, err)
	require.Equal(t, expired.ResourceCandidate, renewed.Definition.Resource.ResourceCandidate)
	require.Equal(t, expired.AuthorityBinding, renewed.Definition.Resource.AuthorityBinding)
	private := expired
	private.ProviderIdentity = "foreign"
	_, err = rt.PrimitiveWindows.RenewDefaultPresentation(context.Background(), "original", private, window.ResourceTarget)
	require.ErrorIs(t, err, identity.ErrResourceDenied)
	require.NoError(t, os.WriteFile(file, append(raw, []byte("chipName: changed\n")...), 0600))
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", "/window/original?applyPermission=true", nil))
	require.NotEqual(t, 200, response.Code, "source change cannot fall back to direct YAML")
}
