package sdk_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/stretchr/testify/require"
	uihttp "github.com/viant/agently-core/adapter/http/ui"
	"github.com/viant/agently-core/app/executor"
	"github.com/viant/agently-core/app/executor/config"
	appserver "github.com/viant/agently-core/app/server"
	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcp/manager"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/agently-core/sdk"
	"github.com/viant/agently-core/workspace"
	"github.com/viant/forge/backend/types"
	"github.com/viant/mcp"
	"github.com/viant/mcp-protocol/schema"
	mcpclient "github.com/viant/mcp/client"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

type presentationHTTPAuth struct{}

type presentationManagingClient struct {
	mcpclient.Interface
	calls atomic.Int64
	t     *testing.T
}

func (c *presentationManagingClient) Initialize(context.Context, ...mcpclient.RequestOption) (*schema.InitializeResult, error) {
	return &schema.InitializeResult{ProtocolVersion: schema.LatestProtocolVersion}, nil
}
func (c *presentationManagingClient) ListTools(context.Context, *string, ...mcpclient.RequestOption) (*schema.ListToolsResult, error) {
	return &schema.ListToolsResult{Tools: []schema.Tool{{Name: "read", InputSchema: schema.ToolInputSchema{Type: "object"}}}}, nil
}
func (c *presentationManagingClient) CallTool(ctx context.Context, in *schema.CallToolRequestParams, _ ...mcpclient.RequestOption) (*schema.CallToolResult, error) {
	require.Equal(c.t, "read", in.Name)
	require.Equal(c.t, "990001", fmt.Sprint(in.Arguments["AdvertiserId"]))
	require.Equal(c.t, "current-sdk-credential", ctx.Value(presentationHTTPAuth{}))
	c.calls.Add(1)
	return &schema.CallToolResult{StructuredContent: map[string]any{"data": []any{map[string]any{"id": "normal"}}}}, nil
}

func TestNormalNativeWindowHTTPOpenBindsSessionBeforeRolloverData(t *testing.T) {
	for _, key := range []string{"AGENTLY_DB_DRIVER", "AGENTLY_DB_DSN", "AGENTLY_DB_PATH", "AGENTLY_DB_SECRETS"} {
		t.Setenv(key, "")
	}
	previousWorkspace := workspace.Root()
	t.Cleanup(func() { workspace.SetRoot(previousWorkspace) })
	root := t.TempDir()
	files := map[string]string{
		"extension/forge/windows/original.yaml":    "id: original\ntitle: Original YAML Title\nnamespace: Unrelated Display Namespace\nwindowKey: original\nview: {content: {id: original}}\nresources: {dataSources: [records]}\n",
		"extension/forge/datasources/records.yaml": "id: records\nselectors: {data: data}\nbackend: {kind: mcp_tool, service: managing, method: read}\n",
	}
	for file, body := range files {
		path := filepath.Join(root, file)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
		require.NoError(t, os.WriteFile(path, []byte(body), 0600))
	}
	managing := &presentationManagingClient{t: t}
	mgr, err := manager.New(nil)
	require.NoError(t, err)
	require.NoError(t, mgr.RegisterLocal(context.Background(), "managing", &mcpcfg.MCPClient{PrimitiveProviderIdentity: "managing", ClientOptions: &mcp.ClientOptions{}}, func(context.Context) (mcpclient.Interface, error) { return managing, nil }))
	rt, backend, _, err := appserver.BuildWorkspaceRuntime(context.Background(), appserver.RuntimeOptions{WorkspaceRoot: root, Defaults: &config.Defaults{}, SkipRegistryInitialize: true, ConfigureBuilder: func(_ context.Context, b *executor.Builder) error { b.WithMCPManager(mgr); return nil }})
	require.NoError(t, err)
	defer rt.Close(context.Background())
	originalActor := rt.PrimitiveProviders.Actor
	cutoff := time.Now().Add(500 * time.Millisecond)
	var fresh atomic.Bool
	rt.PrimitiveProviders.Actor = func(ctx context.Context) (identity.VerifiedActor, error) {
		a, err := originalActor(ctx)
		if !fresh.Load() {
			a.ValidUntil = cutoff
		}
		return a, err
	}
	metadata := uihttp.NewEmbeddedHandlerWithAuthorization(root, nil, nil, nil, uihttp.WithWindowResourceProvider(rt.UIBridge))
	api := sdk.NewHandler(backend, sdk.WithUIBridgeHandler(http.HandlerFunc(rt.UIBridge.Hub().ServeHTTPRPC)))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/window/original" {
			metadata.ServeHTTP(w, r)
			return
		}
		api.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), presentationHTTPAuth{}, "current-sdk-credential")))
	}))
	defer server.Close()
	get := func(query url.Values, session string) *types.Window {
		req, err := http.NewRequest("GET", server.URL+"/window/original?"+query.Encode(), nil)
		require.NoError(t, err)
		if session != "" {
			req.Header.Set("Mcp-Session-Id", session)
		}
		response, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer response.Body.Close()
		raw, err := io.ReadAll(response.Body)
		require.NoError(t, err)
		require.Equal(t, 200, response.StatusCode, string(raw))
		var output struct {
			Data types.Window `json:"data"`
		}
		require.NoError(t, json.Unmarshal(raw, &output))
		return &output.Data
	}
	query := url.Values{"applyPermission": {"true"}, "platform": {"web"}, "windowParams": {`{"AdvertiserId":[990001]}`}}
	first := get(query, "")
	require.Equal(t, "Original YAML Title", first.Title)
	hello := bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"ui.hello","params":{"clientId":"owned-client"}}`)
	req, err := http.NewRequest("POST", server.URL+"/v1/ui/rpc", hello)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	response, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	require.NoError(t, err)
	require.Equal(t, 200, response.StatusCode, string(body))
	session := response.Header.Get("Mcp-Session-Id")
	require.NotEmpty(t, session)
	// The existing native store assigns this local instance after the first GET.
	query.Set("clientId", "owned-client")
	query.Set("windowId", "standalone:root-instance")
	pinJSON, _ := json.Marshal(first.Resource)
	query.Set("resolvedResource", string(pinJSON))
	second := get(query, session)
	require.Equal(t, *first.Resource, *second.Resource)
	require.True(t, types.SameWindowTarget(first.ResourceTarget, second.ResourceTarget))
	require.Equal(t, first.Title, second.Title)
	fresh.Store(true)
	time.Sleep(time.Until(cutoff) + 10*time.Millisecond)
	fetchBody, _ := json.Marshal(map[string]any{"resource": first.Resource, "target": first.ResourceTarget, "inputs": map[string]any{"AdvertiserId": 990001}})
	req, err = http.NewRequest("POST", server.URL+"/v1/api/datasources/records/fetch?windowId=standalone:root-instance", bytes.NewReader(fetchBody))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Mcp-Session-Id", session)
	response, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	body, err = io.ReadAll(response.Body)
	response.Body.Close()
	require.NoError(t, err)
	require.Equal(t, 200, response.StatusCode, string(body))
	require.Contains(t, string(body), "normal")
	require.EqualValues(t, 1, managing.calls.Load())
}
