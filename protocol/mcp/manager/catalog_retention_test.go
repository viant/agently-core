package manager

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	authctx "github.com/viant/agently-core/internal/auth"
	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/mcp"
	mcpclient "github.com/viant/mcp/client"
)

// Exercise Core's actual host default against an ordinary HTTP MCP whose
// metadata has zero TTL. Neither a browser cache nor a request snapshot exists.
func TestHostRetainsPrivateCatalogAcrossIndependentRequests(t *testing.T) {
	var reads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var request struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		if request.ID == nil {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		var result any
		switch request.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{}, "serverInfo": map[string]any{"name": "ordinary", "version": "1"}}
		case "tools/list":
			reads.Add(1)
			result = map[string]any{"cacheScope": "private", "ttlMs": 0, "tools": []any{map[string]any{"name": "read", "description": r.Header.Get("Authorization"), "inputSchema": map[string]any{"type": "object"}}}}
		default:
			t.Errorf("unexpected method %s", request.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Mcp-Session-Id", "catalog-session")
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result}))
	}))
	defer server.Close()
	cfg := &mcpcfg.MCPClient{ClientOptions: &mcp.ClientOptions{ProtocolVersion: "2025-11-25", Transport: mcp.ClientTransport{Type: "streamable", ClientTransportHTTP: mcp.ClientTransportHTTP{URL: server.URL}}}}
	manager, err := New(&authTokenProviderConfigStub{cfg: cfg})
	require.NoError(t, err)
	defer manager.CloseConversation("connection-alice")
	defer manager.CloseConversation("connection-bob")
	for _, user := range []string{"alice", "bob", "alice", "bob"} {
		ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: user})
		client, err := manager.Get(ctx, "connection-"+user, "ordinary")
		require.NoError(t, err)
		result, err := client.ListTools(ctx, nil, mcpclient.WithAuthToken(user))
		require.NoError(t, err)
		require.Len(t, result.Tools, 1)
		require.Equal(t, "Bearer "+user, *result.Tools[0].Description)
	}
	require.EqualValues(t, 2, reads.Load(), "one source read per authenticated user, not per request")
}
