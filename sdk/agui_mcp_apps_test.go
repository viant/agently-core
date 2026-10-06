package sdk

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/runtime/mcpapps"
	mcpschema "github.com/viant/mcp-protocol/schema"
)

func TestMCPAppsProxyUsesPublishedContractAndFailClosedAppScope(t *testing.T) {
	input, handled, err := ParseMCPAppsProxyRequest(json.RawMessage(`{"__proxiedMCPRequest":{"serverHash":"hash","serverId":"configured","method":"tools/call","params":{"name":"widget","arguments":{"n":1}}}}`))
	require.NoError(t, err)
	require.True(t, handled)
	_, _, err = dispatchMCPAppsProxy(context.Background(), "operation", input)
	require.Error(t, err)
	calls := 0
	binding := AGUIMCPAppsBindings{App: AGUIMCPAppBinding{AppInstanceID: "host-instance", ThreadID: "original-thread", ServerID: "configured", ServerHash: "hash", ResourceURI: "ui://configured/widget"}, Authorize: func(context.Context, AGUIMCPAppBinding) error { return nil }, AuthorizeTool: func(_ context.Context, app AGUIMCPAppBinding, name string) error {
		require.Equal(t, "original-thread", app.ThreadID)
		require.Equal(t, "widget", name)
		return nil
	}}
	binding.ToolCaller = func(ctx context.Context, input *MCPUIToolCallInput) (*MCPUIToolCallOutput, error) {
		calls++
		require.Equal(t, "original-thread", input.ConversationID)
		require.Equal(t, "configured/widget", input.ToolName)
		var full mcpschema.CallToolResult
		require.NoError(t, json.Unmarshal([]byte(`{"resultType":"complete","content":[{"type":"text","text":"model-safe"}],"structuredContent":{"view":1},"_meta":{"host-secret":{"ids":[1,2]}},"isError":false}`), &full))
		require.NoError(t, mcpapps.Record(ctx, "configured", "widget", "native-tool-id", &full))
		return &MCPUIToolCallOutput{Status: "ok", Result: "model-safe"}, nil
	}
	ctx := WithAGUIMCPAppsBindings(context.Background(), binding)
	result, pending, err := dispatchMCPAppsProxy(ctx, "operation", input)
	require.NoError(t, err)
	require.Nil(t, pending)
	require.Contains(t, string(result), `"_meta"`)
	require.Contains(t, string(result), `host-secret`)
	require.Equal(t, 1, calls)
	activity, err := MCPAppsActivity("activity", binding.App, result, json.RawMessage(`{"n":1}`))
	require.NoError(t, err)
	require.Contains(t, string(activity), `"activityType":"mcp-apps"`)
	require.Contains(t, string(activity), `"resourceUri":"ui://configured/widget"`)
	input.ServerID = "untrusted-url"
	_, _, err = dispatchMCPAppsProxy(ctx, "operation", input)
	require.Error(t, err)
	require.Equal(t, 1, calls)
}
func TestMCPAppsProxyPendingApprovalIsNotReportedAsMCPCompletion(t *testing.T) {
	binding := AGUIMCPAppsBindings{App: AGUIMCPAppBinding{AppInstanceID: "a", ThreadID: "thread", ServerID: "server", ServerHash: "hash", ResourceURI: "ui://server/a"}, Authorize: func(context.Context, AGUIMCPAppBinding) error { return nil }, AuthorizeTool: func(context.Context, AGUIMCPAppBinding, string) error { return nil }, ToolCaller: func(context.Context, *MCPUIToolCallInput) (*MCPUIToolCallOutput, error) {
		return &MCPUIToolCallOutput{Status: "queued", Result: "queued for user approval"}, nil
	}}
	result, pending, err := dispatchMCPAppsProxy(WithAGUIMCPAppsBindings(context.Background(), binding), "op", &MCPAppsProxyRequest{ServerID: "server", ServerHash: "hash", Method: "tools/call", Params: map[string]json.RawMessage{"name": json.RawMessage(`"effect"`)}})
	require.NoError(t, err)
	require.Nil(t, result)
	require.NotNil(t, pending)
	require.Equal(t, "queued", pending.Status)
	for _, raw := range []string{`{"__proxiedMCPRequest":{"serverHash":"h","method":"roots/list"}}`, `{"__proxiedMCPRequest":{"serverHash":"h","method":"ping","url":"https://untrusted"}}`} {
		_, _, err = ParseMCPAppsProxyRequest(json.RawMessage(raw))
		require.Error(t, err)
	}
}

func TestMCPAppsEndpointHashIsNotAuthority(t *testing.T) {
	hash, err := MCPAppsServerHash("streamable", "https://configured.example/mcp")
	require.NoError(t, err)
	same, err := MCPAppsServerHash("http", "https://configured.example/mcp")
	require.NoError(t, err)
	require.Equal(t, hash, same)
	_, err = MCPAppsServerHash("http", "https://user:secret@configured.example/mcp")
	require.Error(t, err)
	_, err = MCPAppsServerHash("http", "file:///sensitive")
	require.Error(t, err)
}

func TestMCPAppsParserDoesNotRejectOtherStandardForwardedPropsShapes(t *testing.T) {
	for _, raw := range []string{`"opaque"`, `[1,2]`, `42`, `false`, `{"ordinary":"extension"}`} {
		_, handled, err := ParseMCPAppsProxyRequest(json.RawMessage(raw))
		require.NoError(t, err)
		require.False(t, handled)
	}
}

func TestMCPAppsProxyCannotEscalateToolOrResourceScope(t *testing.T) {
	calls := 0
	binding := AGUIMCPAppsBindings{App: AGUIMCPAppBinding{AppInstanceID: "instance", ThreadID: "original", ServerID: "native", PublicServerID: "issued-alias", ServerHash: "hash", ResourceURI: "ui://native/app"}, Authorize: func(context.Context, AGUIMCPAppBinding) error { return nil }, AuthorizeTool: func(context.Context, AGUIMCPAppBinding, string) error { return fmt.Errorf("tool revoked") }, ToolCaller: func(context.Context, *MCPUIToolCallInput) (*MCPUIToolCallOutput, error) { calls++; return nil, nil }, ResourceReader: func(context.Context, string, string) (*mcpschema.ReadResourceResult, error) { calls++; return nil, nil }}
	ctx := WithAGUIMCPAppsBindings(context.Background(), binding)
	for _, input := range []*MCPAppsProxyRequest{
		{ServerID: "native", ServerHash: "hash", Method: "tools/call", Params: map[string]json.RawMessage{"name": json.RawMessage(`"view"`)}},
		{ServerID: "issued-alias", ServerHash: "hash", Method: "tools/call", Params: map[string]json.RawMessage{"name": json.RawMessage(`"forbidden"`)}},
		{ServerID: "issued-alias", ServerHash: "hash", Method: "resources/read", Params: map[string]json.RawMessage{"uri": json.RawMessage(`"ui://native/other"`)}},
		{ServerID: "issued-alias", ServerHash: "hash", Method: "ping"},
		{ServerID: "issued-alias", ServerHash: "hash", Method: "notifications/message"},
	} {
		_, _, err := dispatchMCPAppsProxy(ctx, "op", input)
		require.Error(t, err)
	}
	require.Zero(t, calls)
}
