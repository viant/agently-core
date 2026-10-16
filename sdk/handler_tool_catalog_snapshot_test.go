package sdk

import (
	"context"
	"encoding/json"
	"github.com/viant/agently-core/protocol/mcp/auth/integrate"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	authctx "github.com/viant/agently-core/internal/auth"
	toolregistry "github.com/viant/agently-core/internal/tool/registry"
	cfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcp/manager"
	"github.com/viant/mcp"
)

type httpToolSnapshotProvider struct{ endpoint string }

func (p *httpToolSnapshotProvider) Names(context.Context) ([]string, error) {
	return []string{"steward"}, nil
}
func (p *httpToolSnapshotProvider) Options(context.Context, string) (*cfg.MCPClient, error) {
	return &cfg.MCPClient{ToolsListVisibility: cfg.ToolsListVisibilityPrivate, OutputArtifacts: map[string]cfg.OutputArtifact{"Download": {Name: "file.xlsx"}}, ClientOptions: &mcp.ClientOptions{Name: "fixture", ProtocolVersion: "2025-11-25", Transport: mcp.ClientTransport{Type: "streamable", ClientTransportHTTP: mcp.ClientTransportHTTP{URL: p.endpoint}}, Auth: &mcp.ClientAuth{UseIdToken: true}}}, nil
}
func TestHTTPToolsListingSharesZeroTTLSourceSnapshot(t *testing.T) {
	t.Setenv("AGENTLY_MCP_SERVERS", "steward")
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(405)
			return
		}
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Mcp-Session-Id", "zero-ttl-session")
		var result any
		switch request.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "zero-ttl", "version": "1"}}
		case "tools/list":
			calls.Add(1)
			result = map[string]any{"tools": []any{map[string]any{"name": "Download", "inputSchema": map[string]any{"type": "object"}}}, "cacheScope": "private", "ttlMs": 0}
		default:
			w.WriteHeader(202)
			return
		}
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result}))
	}))
	defer upstream.Close()
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	authTransport, err := integrate.NewAuthRoundTripperWithElicitation(jar, http.DefaultTransport, 0, nil)
	require.NoError(t, err)
	mgr, err := manager.New(&httpToolSnapshotProvider{upstream.URL}, manager.WithCookieJar(jar), manager.WithAuthRoundTripper(authTransport))
	require.NoError(t, err)
	defer mgr.CloseConversation("")
	registry, err := toolregistry.NewWithManager(mgr)
	require.NoError(t, err)
	handler := manager.WithRequestCatalog(handleListToolDefinitions(&backendClient{registry: registry}))
	for index, user := range []string{"alice", "alice", "bob"} {
		ctx := authctx.WithIDToken(authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: user}), "token-"+user)
		out := httptest.NewRecorder()
		request := httptest.NewRequest("GET", "/v1/tools", nil).WithContext(ctx)
		handler.ServeHTTP(out, request)
		require.Equal(t, 200, out.Code, out.Body.String())
		require.Contains(t, out.Body.String(), "owned immutable artifact", "artifact projection must resolve catalog identity after raw listing")
		require.EqualValues(t, index+1, calls.Load(), "one upstream call per HTTP listing; zero-TTL may not cross requests or principals")
	}
}
