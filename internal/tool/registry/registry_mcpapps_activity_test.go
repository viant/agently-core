package tool

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	authctx "github.com/viant/agently-core/internal/auth"
	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/runtime/mcpapps"
	"github.com/viant/agently-core/runtime/requestctx"
	mcp "github.com/viant/mcp"
	mcpschema "github.com/viant/mcp-protocol/schema"
	mcpclient "github.com/viant/mcp/client"
)

func TestRegistryProducesMCPAppOnlyFromRegisteredNativeCatalog(t *testing.T) {
	for _, registered := range []bool{true, false} {
		t.Run(map[bool]string{true: "registered", false: "responseCannotGrantApp"}[registered], func(t *testing.T) {
			remote := &hostResultClient{body: `{"resultType":"complete","content":[{"type":"text","text":"safe"}],"structuredContent":{"host":"private"},"_meta":{"ui":{"resourceUri":"ui://caller/forged"},"token":"private"}}`}
			registry, _ := newRemoteProtectionRegistry(remote, nil)
			registry.mgr = &discoveryManagerStub{options: &mcpcfg.MCPClient{ToolsListVisibility: mcpcfg.ToolsListVisibilityPublic, ClientOptions: &mcp.ClientOptions{Transport: mcp.ClientTransport{Type: "streamable", ClientTransportHTTP: mcp.ClientTransportHTTP{URL: "http://configured.example/mcp"}}}}, getFunc: func(string, string) (mcpclient.Interface, error) { return remote, nil }}
			var definition mcpschema.Tool
			require.NoError(t, json.Unmarshal([]byte(`{"name":"tool","inputSchema":{"type":"object"}}`), &definition))
			if registered {
				require.NoError(t, json.Unmarshal([]byte(`{"name":"tool","inputSchema":{"type":"object"},"_meta":{"ui":{"resourceUri":"ui://native/registered"}}}`), &definition))
			}
			registry.mergeServerTools("service", []mcpschema.Tool{definition})
			ctx := requestctx.WithTurnMeta(authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "owner"}), requestctx.TurnMeta{ConversationID: "original", TurnID: "native-turn"})
			ctx = requestctx.WithConversationID(ctx, "original")
			emitted := 0
			ctx = mcpapps.WithActivityEmitter(ctx, func(_ context.Context, app mcpapps.AppBinding, result, args json.RawMessage) error {
				emitted++
				require.Equal(t, "original", app.ThreadID)
				require.Equal(t, "service", app.ServerID)
				require.Equal(t, "ui://native/registered", app.ResourceURI)
				hash, err := mcpapps.ServerHash("streamable", "http://configured.example/mcp")
				require.NoError(t, err)
				require.Equal(t, hash, app.ServerHash)
				require.Contains(t, string(result), `"token":"private"`)
				require.JSONEq(t, `{"n":1}`, string(args))
				return nil
			})
			text, err := registry.Execute(ctx, "service/tool", map[string]interface{}{"n": 1})
			require.NoError(t, err)
			require.Equal(t, "safe", text)
			if registered {
				require.Equal(t, 1, emitted)
			} else {
				require.Zero(t, emitted)
			}
		})
	}
}
