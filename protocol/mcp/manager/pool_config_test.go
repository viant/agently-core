package manager

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	authctx "github.com/viant/agently-core/internal/auth"
	cfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/jsonrpc"
	"github.com/viant/jsonrpc/transport"
	"github.com/viant/mcp"
	protocolclient "github.com/viant/mcp-protocol/client"
	"github.com/viant/mcp-protocol/logger"
	"github.com/viant/mcp-protocol/schema"
	protocol "github.com/viant/mcp-protocol/server"
	mcpserver "github.com/viant/mcp/server"
)

type changingEndpointProvider struct {
	mu       sync.Mutex
	endpoint string
	removed  bool
}

func (p *changingEndpointProvider) Options(context.Context, string) (*cfg.MCPClient, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.removed {
		return nil, nil
	}
	return &cfg.MCPClient{ClientOptions: &mcp.ClientOptions{Name: "owned", Version: "1", ProtocolVersion: schema.LatestProtocolVersion, Transport: mcp.ClientTransport{Type: "streamable", ClientTransportHTTP: mcp.ClientTransportHTTP{URL: p.endpoint}}}}, nil
}
func (p *changingEndpointProvider) set(endpoint string, removed bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.endpoint = endpoint
	p.removed = removed
}

func poolConfigEndpoint(t *testing.T, label string, started, release chan struct{}) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	calls := &atomic.Int32{}
	server, err := mcpserver.New(mcpserver.WithStreamableURI("/mcp"), mcpserver.WithNewHandler(func(ctx context.Context, n transport.Notifier, l logger.Logger, c protocolclient.Operations) (protocol.Handler, error) {
		h := protocol.NewDefaultHandler(n, l, c)
		h.Registry.RegisterToolWithSchema("owned.echo", "Echo", schema.ToolInputSchema{Type: "object"}, nil, func(_ context.Context, r *schema.CallToolRequest) (*schema.CallToolResult, *jsonrpc.Error) {
			calls.Add(1)
			return &schema.CallToolResult{StructuredContent: map[string]any{"endpoint": label, "arguments": r.Params.Arguments}}, nil
		})
		return h, nil
	}))
	require.NoError(t, err)
	server.UseStreamableHTTP(true)
	handler := server.HTTP(context.Background(), "").Handler
	var once sync.Once
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if started != nil && r.Method == http.MethodPost && r.Body != nil {
			raw, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			r.Body = io.NopCloser(bytes.NewReader(raw))
			var packet struct {
				Method string `json:"method"`
			}
			_ = json.Unmarshal(raw, &packet)
			if packet.Method == "initialize" {
				once.Do(func() { close(started) })
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
			}
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(peer.Close)
	return peer, calls
}

func TestPoolEffectiveConfigReplacesEndpointAndRejectsRemoval(t *testing.T) {
	a, callsA := poolConfigEndpoint(t, "A", nil, nil)
	b, callsB := poolConfigEndpoint(t, "B", nil, nil)
	provider := &changingEndpointProvider{endpoint: a.URL + "/mcp"}
	mgr, err := New(provider)
	require.NoError(t, err)
	defer mgr.CloseConversation("")
	alice := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
	bob := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "bob"})
	first, err := mgr.Get(alice, "", "owned")
	require.NoError(t, err)
	separate, err := mgr.Get(bob, "", "owned")
	require.NoError(t, err)
	require.NotSame(t, first, separate)
	require.False(t, mgr.IsCurrentClient(bob, "", "owned", first))
	args := map[string]any{"filter": "original", "id": "opaque-owned"}
	result, err := first.CallTool(alice, &schema.CallToolRequestParams{Name: "owned.echo", Arguments: args})
	require.NoError(t, err)
	require.Equal(t, "A", result.StructuredContent.(map[string]any)["endpoint"])
	provider.set(b.URL+"/mcp", false)
	replacement, err := mgr.Get(alice, "", "owned")
	require.NoError(t, err)
	require.NotSame(t, first, replacement)
	require.False(t, mgr.IsCurrentClient(alice, "", "owned", first))
	result, err = replacement.CallTool(alice, &schema.CallToolRequestParams{Name: "owned.echo", Arguments: args})
	require.NoError(t, err)
	require.Equal(t, "B", result.StructuredContent.(map[string]any)["endpoint"])
	require.Equal(t, args, result.StructuredContent.(map[string]any)["arguments"])
	provider.set("", true)
	removed, err := mgr.Get(alice, "", "owned")
	require.Error(t, err)
	require.Nil(t, removed)
	require.False(t, mgr.IsCurrentClient(alice, "", "owned", replacement))
	require.EqualValues(t, 1, callsA.Load())
	require.EqualValues(t, 1, callsB.Load(), "configuration replacement cannot replay tools")
}
