package datasource_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	authctx "github.com/viant/agently-core/internal/auth"
	registry "github.com/viant/agently-core/internal/tool/registry"
	dsproto "github.com/viant/agently-core/protocol/datasource"
	"github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcp/manager"
	"github.com/viant/agently-core/service/datasource"
	"github.com/viant/agently-core/service/datasource/adapter"
	"github.com/viant/forge/backend/types"
	"github.com/viant/mcp"
	"github.com/viant/mcp-protocol/schema"
)

type sessionRecoveryOptions struct{ endpoint string }

func (p sessionRecoveryOptions) Options(context.Context, string) (*config.MCPClient, error) {
	bff := false
	return &config.MCPClient{ClientOptions: &mcp.ClientOptions{Name: "example-reader", Version: "1", Auth: &mcp.ClientAuth{BackendForFrontend: &bff}, Transport: mcp.ClientTransport{Type: "streamable", ClientTransportHTTP: mcp.ClientTransportHTTP{URL: p.endpoint}}, PingIntervalSeconds: 3600}}, nil
}

// Independently constructed fixture: the server restarts between datasource
// reads. Its expired-session gate rejects before the tool business handler.
func TestDatasourceReadRecoversExpiredPooledMCPSessionWithoutCatalogRefresh(t *testing.T) {
	t.Setenv("AGENTLY_WORKSPACE", t.TempDir())
	var initializations, catalogs, wireCalls, executions atomic.Int64
	var current atomic.Value
	current.Store("")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var q struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			} `json:"params"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&q))
		reply := func(result any) {
			w.Header().Set("Content-Type", "application/json")
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": q.ID, "result": result}))
		}
		if q.Method == schema.MethodInitialize {
			require.Empty(t, r.Header.Get("Mcp-Session-Id"))
			id := fmt.Sprintf("session-%d", initializations.Add(1))
			current.Store(id)
			w.Header().Set("Mcp-Session-Id", id)
			reply(map[string]any{"protocolVersion": schema.LegacyProtocolVersion, "capabilities": map[string]any{}, "serverInfo": map[string]string{"name": "example", "version": "1"}})
			return
		}
		if q.Method == schema.MethodToolsCall {
			wireCalls.Add(1)
		}
		if r.Header.Get("Mcp-Session-Id") != current.Load().(string) {
			http.Error(w, "endpoint unavailable", 404)
			return
		}
		require.Equal(t, schema.LegacyProtocolVersion, r.Header.Get(schema.HeaderProtocolVersion))
		switch q.Method {
		case schema.MethodNotificationInitialized:
			w.WriteHeader(http.StatusAccepted)
		case schema.MethodToolsList:
			catalogs.Add(1)
			reply(map[string]any{"tools": []any{map[string]any{"name": "ListRows", "inputSchema": map[string]any{"type": "object"}}}})
		case schema.MethodToolsCall:
			require.Equal(t, "ListRows", q.Params.Name)
			require.Equal(t, "selected", q.Params.Arguments["query"])
			executions.Add(1)
			reply(map[string]any{"content": []any{}, "structuredContent": map[string]any{"data": []any{map[string]any{"id": 7, "name": "Example row"}}}})
		default:
			t.Errorf("unexpected MCP method %s", q.Method)
			w.WriteHeader(400)
		}
	}))
	defer server.Close()
	mgr, err := manager.New(sessionRecoveryOptions{server.URL})
	require.NoError(t, err)
	defer mgr.CloseConversation("")
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "example-reader"})
	pooled, err := mgr.Get(ctx, "", "example-reader")
	require.NoError(t, err)
	_, err = pooled.ListTools(ctx, nil)
	require.NoError(t, err)
	reg, err := registry.NewWithManager(mgr)
	require.NoError(t, err)
	reg.SetResultReuseDisabled(true)
	store := datasource.NewMemoryStore()
	ds := &dsproto.DataSource{ID: "rows", Backend: &dsproto.Backend{Kind: dsproto.BackendMCPTool, Service: "example-reader", Method: "ListRows"}}
	ds.DataSource = types.DataSource{Selectors: &types.Selectors{Data: "data"}}
	store.Put(ds)
	svc := datasource.New(datasource.Options{Store: store, Executor: adapter.FromRegistry(reg)})
	for i := 0; i < 2; i++ {
		if i == 1 {
			current.Store("discarded-session")
		}
		result, err := svc.Fetch(ctx, "rows", map[string]any{"query": "selected"}, datasource.FetchOptions{BypassCache: true})
		require.NoError(t, err)
		require.Len(t, result.Rows, 1)
		require.Equal(t, "Example row", result.Rows[0]["name"])
	}
	require.EqualValues(t, 2, initializations.Load())
	require.EqualValues(t, 3, wireCalls.Load())
	require.EqualValues(t, 2, executions.Load())
	require.EqualValues(t, 1, catalogs.Load(), "data reads and session recovery must not re-fetch the catalog")
	again, err := mgr.Get(ctx, "", "example-reader")
	require.NoError(t, err)
	require.Same(t, pooled, again, "recovery retains the existing pooled client")
}
