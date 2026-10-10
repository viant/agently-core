package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcpname"
	"github.com/viant/agently-core/runtime/mcpapps"
	mcpschema "github.com/viant/mcp-protocol/schema"
	mcpclient "github.com/viant/mcp/client"
)

type hostResultClient struct {
	mcpclient.Interface
	calls int
	body  string
}

func (c *hostResultClient) CallTool(context.Context, *mcpschema.CallToolRequestParams, ...mcpclient.RequestOption) (*mcpschema.CallToolResult, error) {
	c.calls++
	var result mcpschema.CallToolResult
	body := c.body
	if body == "" {
		body = `{"resultType":"complete","content":[{"type":"text","text":"model-safe"}],"structuredContent":{"data":1},"_meta":{"host":{"secret":"opaque"}},"isError":false}`
	}
	err := json.Unmarshal([]byte(body), &result)
	return &result, err
}
func TestRegistryMCPAppsCaptureKeepsHostOnlyMetadataOutOfModelText(t *testing.T) {
	client := &hostResultClient{}
	registry, _ := newAdvertisedAppCaptureRegistry(client)
	ctx, capture := mcpapps.WithCapture(protectedTurnContext("native-turn"), "service", "tool", "host-operation")
	text, err := registry.Execute(ctx, "service/tool", map[string]interface{}{})
	require.NoError(t, err)
	require.Equal(t, "model-safe", text)
	full, _ := capture.Snapshot()
	require.Contains(t, string(full), `"_meta"`)
	require.Contains(t, string(full), `opaque`)
	require.NotContains(t, text, "opaque")
	// A second independently scoped UI request must not adopt model-text-only cache.
	ctx, next := mcpapps.WithCapture(protectedTurnContext("native-turn"), "service", "tool", "host-operation-2")
	_, err = registry.Execute(ctx, "service/tool", map[string]interface{}{})
	require.NoError(t, err)
	raw, _ := next.Snapshot()
	require.NotEmpty(t, raw)
	require.Equal(t, 2, client.calls)
}

func TestRegistryMCPAppsNeverRetriesAmbiguousRemoteEffect(t *testing.T) {
	client := &protectionTestClient{err: errors.New("session not found")}
	registry, manager := newAdvertisedAppCaptureRegistry(client)
	ctx, _ := mcpapps.WithCapture(protectedTurnContext("native-turn"), "service", "tool", "op")
	_, err := registry.Execute(ctx, "service/tool", map[string]interface{}{})
	require.Error(t, err)
	require.Equal(t, int64(1), client.calls.Load())
	_ = manager
}
func TestRegistryMCPAppsRawHostContentNeverBecomesNativeText(t *testing.T) {
	client := &rawResultProtectionClient{}
	registry, _ := newAdvertisedAppCaptureRegistry(client)
	ctx, capture := mcpapps.WithCapture(protectedTurnContext("native-turn"), "service", "tool", "op")
	text, err := registry.Execute(ctx, "service/tool", map[string]interface{}{})
	require.NoError(t, err)
	require.Empty(t, text)
	result, _ := capture.Snapshot()
	require.Contains(t, string(result), `"ok":true`)
}

func TestRegistryMCPAppsStructuredOnlyResultNeverEntersNativeText(t *testing.T) {
	client := &hostResultClient{body: `{"resultType":"complete","content":[],"structuredContent":{"private":"host-only"},"_meta":{"private":"opaque"}}`}
	registry, _ := newAdvertisedAppCaptureRegistry(client)
	ctx, capture := mcpapps.WithCapture(protectedTurnContext("native-turn"), "service", "tool", "op")
	text, err := registry.Execute(ctx, "service/tool", map[string]interface{}{})
	require.NoError(t, err)
	require.Empty(t, text)
	result, _ := capture.Snapshot()
	require.Contains(t, string(result), "host-only")
	require.Contains(t, string(result), "opaque")
}

type literalMCPAppsClient struct {
	mcpclient.Interface
	called string
}

func (c *literalMCPAppsClient) CallTool(_ context.Context, input *mcpschema.CallToolRequestParams, _ ...mcpclient.RequestOption) (*mcpschema.CallToolResult, error) {
	c.called = input.Name
	return &mcpschema.CallToolResult{ResultType: mcpschema.ResultTypeComplete, Content: []mcpschema.CallToolResultContentElem{&mcpschema.TextContent{Type: "text", Text: "safe"}}}, nil
}
func TestRegistryMCPAppsScopedCapturePreservesLiteralRemoteIdentity(t *testing.T) {
	for _, tc := range []struct{ server, method string }{{"server_name", "literal.tool-with_parts"}, {"server-name", "日本語.tool-with-hyphens"}, {"server/path", "method_with.parts-name"}} {
		t.Run(tc.server+"/"+tc.method, func(t *testing.T) {
			remote := &literalMCPAppsClient{}
			registry, _ := newRemoteProtectionRegistry(remote, nil)
			registry.mgr = &discoveryManagerStub{options: &mcpcfg.MCPClient{ToolsListVisibility: mcpcfg.ToolsListVisibilityPublic}, getFunc: func(_ string, server string) (mcpclient.Interface, error) {
				require.Equal(t, tc.server, server)
				return remote, nil
			}}
			registry.mergeServerTools(tc.server, []mcpschema.Tool{{Name: tc.method}})
			ctx, capture := mcpapps.WithCapture(protectedTurnContext("native-turn"), tc.server, tc.method, "host-op")
			value, err := registry.Execute(ctx, mcpname.Canonical(tc.server+"/"+tc.method), map[string]interface{}{})
			require.NoError(t, err)
			require.Equal(t, "safe", value)
			require.Equal(t, tc.method, remote.called)
			result, _ := capture.Snapshot()
			require.NotEmpty(t, result)
		})
	}
}

type literalHTTPMCPAppsClient struct {
	mcpclient.Interface
	endpoint string
}

func (c *literalHTTPMCPAppsClient) CallTool(ctx context.Context, input *mcpschema.CallToolRequestParams, _ ...mcpclient.RequestOption) (*mcpschema.CallToolResult, error) {
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": input})
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	var envelope struct {
		Result mcpschema.CallToolResult `json:"result"`
	}
	if err = json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		return nil, err
	}
	return &envelope.Result, nil
}
func TestRegistryMCPAppsLiteralMethodIsObservedByHTTPServer(t *testing.T) {
	const server = "configured_server-name"
	const method = "日本語.tool-with_parts"
	observed := make(chan string, 1)
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Params struct {
				Name string `json:"name"`
			} `json:"params"`
		}
		if json.NewDecoder(r.Body).Decode(&request) != nil {
			http.Error(w, "bad request", 400)
			return
		}
		observed <- request.Params.Name
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"resultType":"complete","content":[{"type":"text","text":"safe"}],"_meta":{"private":"native-host"}}}`)
	}))
	defer fixture.Close()
	remote := &literalHTTPMCPAppsClient{endpoint: fixture.URL}
	registry, _ := newRemoteProtectionRegistry(remote, nil)
	registry.mgr = &discoveryManagerStub{options: &mcpcfg.MCPClient{ToolsListVisibility: mcpcfg.ToolsListVisibilityPublic}, getFunc: func(_ string, actual string) (mcpclient.Interface, error) {
		require.Equal(t, server, actual)
		return remote, nil
	}}
	registry.mergeServerTools(server, []mcpschema.Tool{{Name: method}})
	ctx, capture := mcpapps.WithCapture(protectedTurnContext("native-turn"), server, method, "host-op")
	text, err := registry.Execute(ctx, mcpname.Canonical(server+"/"+method), nil)
	require.NoError(t, err)
	require.Equal(t, "safe", text)
	require.Equal(t, method, <-observed)
	result, _ := capture.Snapshot()
	require.Contains(t, string(result), `"private":"native-host"`)
}

// Host capture fixtures provide an advertised catalog just like the trusted
// production host. A scoped capture alone cannot grant an unadvertised tool.
func newAdvertisedAppCaptureRegistry(remote mcpclient.Interface) (*Registry, *protectionTestManager) {
	registry, original := newRemoteProtectionRegistry(remote, nil)
	registry.mgr = &discoveryManagerStub{options: &mcpcfg.MCPClient{ToolsListVisibility: mcpcfg.ToolsListVisibilityPublic}, getFunc: func(string, string) (mcpclient.Interface, error) { return remote, nil }}
	registry.mergeServerTools("service", []mcpschema.Tool{{Name: "tool"}})
	return registry, original
}
