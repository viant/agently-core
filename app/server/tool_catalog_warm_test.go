package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	authctx "github.com/viant/agently-core/internal/auth"
	toolregistry "github.com/viant/agently-core/internal/tool/registry"
	integrate "github.com/viant/agently-core/protocol/mcp/auth/integrate"
	config "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcp/manager"
	"github.com/viant/mcp"
)

type warmTestManager struct {
	options    map[string]*config.MCPClient
	namesCalls atomic.Int64
}

func (m *warmTestManager) Names(context.Context) ([]string, error) {
	m.namesCalls.Add(1)
	return []string{"excluded", "unlinked", "legacy", "browser", "linked", "plain"}, nil
}
func (m *warmTestManager) Options(_ context.Context, name string) (*config.MCPClient, error) {
	return m.options[name], nil
}
func (m *warmTestManager) PreflightCredential(_ context.Context, name string) error {
	if name == "unlinked" {
		return errors.New("link required")
	}
	return nil
}
func (m *warmTestManager) WithAuthTokenContext(ctx context.Context, _ string) context.Context {
	return ctx
}

type warmInvocation struct {
	ctx   context.Context
	names []string
}
type recordingCatalogWarmer struct {
	calls   chan warmInvocation
	release chan struct{}
}

func (w *recordingCatalogWarmer) WarmToolCatalogs(ctx context.Context, names []string) error {
	w.calls <- warmInvocation{ctx: ctx, names: names}
	select {
	case <-w.release:
	case <-ctx.Done():
	}
	return nil
}
func TestAuthenticatedCatalogWarmupPreservesCallerSkipsUnauthorizedAndCoalescesPolls(t *testing.T) {
	owner := &warmTestManager{options: map[string]*config.MCPClient{}}
	plain := &config.MCPClient{ClientOptions: &mcp.ClientOptions{}}
	delegated := &config.MCPClient{ClientOptions: &mcp.ClientOptions{Auth: &mcp.ClientAuth{Mode: "oauth", ProviderRef: "provider"}}}
	legacy := &config.MCPClient{ClientOptions: &mcp.ClientOptions{Auth: &mcp.ClientAuth{OAuth2ConfigURL: []string{"legacy-config"}}}}
	owner.options = map[string]*config.MCPClient{"excluded": plain, "unlinked": delegated, "legacy": legacy, "browser": {ExecutionLocation: "browser", ClientOptions: &mcp.ClientOptions{}}, "linked": delegated, "plain": plain}
	warmer := &recordingCatalogWarmer{calls: make(chan warmInvocation, 4), release: make(chan struct{})}
	defer close(warmer.release)
	handler := withAuthenticatedToolCatalogWarmup(context.Background(), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }), warmer, owner, ToolCatalogWarmOptions{ExcludedServers: []string{"excluded"}, Interval: time.Nanosecond})
	anonymous := httptest.NewRequest("GET", "/v1/events", nil)
	anonymous.Header.Set("Authorization", "Bearer unverified-header")
	handler.ServeHTTP(httptest.NewRecorder(), anonymous)
	require.Zero(t, owner.namesCalls.Load(), "anonymous requests cannot schedule catalog discovery")
	ctx, cancel := context.WithCancel(authctx.WithBearer(authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"}), "caller-token"))
	request := httptest.NewRequest("GET", "/v1/events", nil).WithContext(ctx)
	handler.ServeHTTP(httptest.NewRecorder(), request)
	cancel()
	select {
	case invocation := <-warmer.calls:
		require.Equal(t, "alice", authctx.EffectiveUserID(invocation.ctx))
		require.Equal(t, "caller-token", authctx.MCPAuthToken(invocation.ctx, false))
		require.NoError(t, invocation.ctx.Err(), "HTTP response completion must not cancel runtime-owned warmup")
		require.Equal(t, []string{"linked", "plain"}, invocation.names)
	case <-time.After(3 * time.Second):
		t.Fatal("authenticated request did not warm eligible connections")
	}
	for i := 0; i < 50; i++ {
		handler.ServeHTTP(httptest.NewRecorder(), request)
	}
	require.EqualValues(t, 1, owner.namesCalls.Load(), "polls must not spawn duplicate in-flight warming even after scheduling interval expires")
}

type warmHTTPProvider struct{ options *config.MCPClient }

func (p *warmHTTPProvider) Names(context.Context) ([]string, error) { return []string{"remote"}, nil }
func (p *warmHTTPProvider) Options(context.Context, string) (*config.MCPClient, error) {
	return p.options, nil
}
func TestAuthenticatedCatalogWarmupUsesExistingManagerSourceCache(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		name := "explicit-ttl"
		if legacy {
			name = "legacy-without-cache-hints"
		}
		t.Run(name, func(t *testing.T) {
			var lists atomic.Int64
			source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" {
					w.WriteHeader(405)
					return
				}
				var request struct {
					ID     json.RawMessage `json:"id"`
					Method string          `json:"method"`
				}
				require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
				require.Equal(t, "Bearer caller-token", r.Header.Get("Authorization"), "warmup must forward the verified caller's token")
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Mcp-Session-Id", "warm-session")
				var result any
				switch request.Method {
				case "initialize":
					result = map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "warm", "version": "1"}}
				case "tools/list":
					lists.Add(1)
					result = map[string]any{"tools": []any{map[string]any{"name": "read", "inputSchema": map[string]any{"type": "object"}}}, "cacheScope": "private", "ttlMs": 60000}
				default:
					w.WriteHeader(202)
					return
				}
				if legacy && request.Method == "tools/list" {
					wire := result.(map[string]any)
					delete(wire, "cacheScope")
					delete(wire, "ttlMs")
				}
				require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result}))
			}))
			defer source.Close()
			provider := &warmHTTPProvider{options: &config.MCPClient{ToolsListVisibility: config.ToolsListVisibilityPrivate, ClientOptions: &mcp.ClientOptions{ProtocolVersion: "2025-11-25", Auth: &mcp.ClientAuth{}, Transport: mcp.ClientTransport{Type: "streamable", ClientTransportHTTP: mcp.ClientTransportHTTP{URL: source.URL}}}}}
			owner, err := manager.New(provider)
			require.NoError(t, err)
			registry, err := toolregistry.NewWithManager(owner)
			require.NoError(t, err)
			lifetime, cancel := context.WithCancel(context.Background())
			defer cancel()
			handler := withAuthenticatedToolCatalogWarmup(lifetime, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }), registry, owner, ToolCatalogWarmOptions{})
			ctx := authctx.WithBearer(authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"}), "caller-token")
			request := httptest.NewRequest("GET", "/v1/events", nil).WithContext(ctx)
			handler.ServeHTTP(httptest.NewRecorder(), request)
			require.Eventually(t, func() bool { return lists.Load() == 1 }, 3*time.Second, 10*time.Millisecond)
			for i := 0; i < 20; i++ {
				handler.ServeHTTP(httptest.NewRecorder(), request)
			}
			require.Len(t, registry.DefinitionsWithContext(ctx), 1)
			require.Len(t, registry.DefinitionsWithContext(ctx), 1)
			require.EqualValues(t, 1, lists.Load(), "polls and aggregate listings must reuse the already warmed manager catalog")
			owner.CloseConversation("")
		})
	}
}

func TestProactiveCatalogWarmupDoesNotBeginBFFLoginOnRejectedCallerToken(t *testing.T) {
	var prompts, requests atomic.Int64
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("WWW-Authenticate", `Bearer realm="fixture", authorization_uri="https://login.example/authorize"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer source.Close()
	authRT, err := integrate.NewAuthRoundTripperWithElicitation(nil, http.DefaultTransport, 0, func(context.Context, string) error { prompts.Add(1); return errors.New("must not prompt") })
	require.NoError(t, err)
	provider := &warmHTTPProvider{options: &config.MCPClient{ToolsListVisibility: config.ToolsListVisibilityPrivate, ClientOptions: &mcp.ClientOptions{Auth: &mcp.ClientAuth{}, Transport: mcp.ClientTransport{Type: "streamable", ClientTransportHTTP: mcp.ClientTransportHTTP{URL: source.URL}}}}}
	owner, err := manager.New(provider, manager.WithAuthRoundTripper(authRT))
	require.NoError(t, err)
	registry, err := toolregistry.NewWithManager(owner)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(authctx.WithBearer(authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"}), "rejected-token"), 2*time.Second)
	defer cancel()
	require.Error(t, registry.WarmToolCatalogs(ctx, []string{"remote"}))
	require.Positive(t, requests.Load())
	require.Zero(t, prompts.Load(), "proactive metadata preparation must not surface an OAuth login")
	owner.CloseConversation("")
}
