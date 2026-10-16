package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"

	"testing"
	"time"

	"github.com/stretchr/testify/require"
	authctx "github.com/viant/agently-core/internal/auth"
	toolregistry "github.com/viant/agently-core/internal/tool/registry"
	integrate "github.com/viant/agently-core/protocol/mcp/auth/integrate"
	config "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcp/manager"
	"github.com/viant/agently-core/protocol/primitive"
	identity "github.com/viant/agently-core/protocol/resource"
	resources "github.com/viant/agently-core/service/resource"
	"github.com/viant/mcp"
	authconfig "github.com/viant/mcp/client/auth/config"
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
	handler := withAuthenticatedToolCatalogWarmup(context.Background(), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }), warmer, owner, ToolCatalogWarmOptions{ExcludedServers: []string{"excluded"}, Interval: time.Nanosecond}, nil)
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
			handler := withAuthenticatedToolCatalogWarmup(lifetime, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }), registry, owner, ToolCatalogWarmOptions{}, nil)
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

type warmToolFunc func(context.Context, []string) error

func (f warmToolFunc) WarmToolCatalogs(ctx context.Context, names []string) error {
	return f(ctx, names)
}

type warmGatewayFunc func(context.Context, string) ([]primitive.NamespaceCapabilities, error)

func (f warmGatewayFunc) DiscoverConnection(ctx context.Context, name string) ([]primitive.NamespaceCapabilities, error) {
	return f(ctx, name)
}

func TestAuthorizedWarmupPreparesOnlyEligibleGatewayConnections(t *testing.T) {
	owner := &warmTestManager{options: map[string]*config.MCPClient{}}
	plain := &config.MCPClient{ClientOptions: &mcp.ClientOptions{}}
	delegated := &config.MCPClient{ClientOptions: &mcp.ClientOptions{Auth: &mcp.ClientAuth{Mode: "oauth", ProviderRef: "provider"}}}
	for _, name := range []string{"excluded", "legacy", "browser", "linked", "plain", "unlinked"} {
		owner.options[name] = plain
	}
	owner.options["unlinked"] = delegated
	owner.options["linked"] = delegated
	owner.options["legacy"] = &config.MCPClient{ClientOptions: &mcp.ClientOptions{Auth: &mcp.ClientAuth{OAuth2ConfigURL: []string{"legacy-config"}}}}
	owner.options["browser"] = &config.MCPClient{ExecutionLocation: "browser", ClientOptions: &mcp.ClientOptions{}}
	calls := make(chan string, 4)
	warmer := warmToolFunc(func(ctx context.Context, names []string) error {
		require.True(t, authconfig.NoRetry(ctx))
		require.Equal(t, "alice", authctx.EffectiveUserID(ctx))
		require.Equal(t, "caller-token", authctx.MCPAuthToken(ctx, false))
		require.Len(t, names, 1)
		calls <- "tools:" + names[0]
		return nil
	})
	gateway := warmGatewayFunc(func(ctx context.Context, name string) ([]primitive.NamespaceCapabilities, error) {
		require.True(t, authconfig.NoRetry(ctx))
		require.Equal(t, "alice", authctx.EffectiveUserID(ctx))
		require.Equal(t, "caller-token", authctx.MCPAuthToken(ctx, false))
		calls <- "gateway:" + name
		return nil, nil
	})
	lifetime, cancel := context.WithCancel(context.Background())
	defer cancel()
	handler := withAuthenticatedToolCatalogWarmup(lifetime, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }), warmer, owner, ToolCatalogWarmOptions{ExcludedServers: []string{"excluded"}}, gateway)
	ctx := authctx.WithBearer(authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"}), "caller-token")
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/v1/events", nil).WithContext(ctx))
	got := []string{}
	for i := 0; i < 4; i++ {
		select {
		case call := <-calls:
			got = append(got, call)
		case <-time.After(3 * time.Second):
			t.Fatal("warmup did not finish")
		}
	}
	require.ElementsMatch(t, []string{"tools:linked", "gateway:linked", "tools:plain", "gateway:plain"}, got)
}

func TestAuthorizedWarmupBoundsWorkAndKeepsProviderFailuresIndependent(t *testing.T) {
	var active, peak atomic.Int64
	entered := make(chan string, 8)
	release := make(chan struct{})
	done := make(chan struct{})
	var mu sync.Mutex
	discovered := []string{}
	warmer := warmToolFunc(func(ctx context.Context, names []string) error {
		n := active.Add(1)
		defer active.Add(-1)
		for previous := peak.Load(); n > previous; previous = peak.Load() {
			if peak.CompareAndSwap(previous, n) {
				break
			}
		}
		entered <- names[0]
		select {
		case <-release:
		case <-ctx.Done():
			return ctx.Err()
		}
		if names[0] == "tools-error" {
			return errors.New("tool discovery failed")
		}
		return nil
	})
	gateway := warmGatewayFunc(func(_ context.Context, name string) ([]primitive.NamespaceCapabilities, error) {
		mu.Lock()
		discovered = append(discovered, name)
		mu.Unlock()
		if name == "gateway-error" {
			return nil, errors.New("namespace discovery failed")
		}
		return nil, nil
	})
	names := []string{"tools-error", "gateway-error", "good-1", "good-2", "good-3", "good-4", "good-5", "good-6"}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go func() { warmAuthorizedCatalogs(ctx, warmer, names, gateway); close(done) }()
	for i := 0; i < 4; i++ {
		select {
		case <-entered:
		case <-ctx.Done():
			t.Fatal("work not parallel")
		}
	}
	require.EqualValues(t, 4, peak.Load())
	close(release)
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("one provider blocked independent warmup")
	}
	require.EqualValues(t, 4, peak.Load())
	mu.Lock()
	defer mu.Unlock()
	require.NotContains(t, discovered, "tools-error")
	require.ElementsMatch(t, names[1:], discovered)
	canceled, stop := context.WithCancel(context.Background())
	stop()
	warmAuthorizedCatalogs(canceled, warmToolFunc(func(context.Context, []string) error { t.Error("canceled tools dispatched"); return nil }), names, gateway)
}

func TestAuthorizedWarmupZeroTTLToolSnapshotPopulatesExistingGatewayCache(t *testing.T) {
	var mu sync.Mutex
	lists := map[string]int{}
	namespaceReads := map[string]int{}
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(405)
			return
		}
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				Name string `json:"name"`
			} `json:"params"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		token := r.Header.Get("Authorization")
		require.Contains(t, []string{"Bearer alice-token", "Bearer bob-token"}, token)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Mcp-Session-Id", "fixture-session")
		var result any
		switch request.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "warm", "version": "1"}}
		case "tools/list":
			mu.Lock()
			lists[token]++
			mu.Unlock()
			tools := []any{}
			for _, name := range []string{"namespaces/list", "namespaces/get"} {
				tools = append(tools, map[string]any{"name": name, "inputSchema": map[string]any{"type": "object"}, "_meta": map[string]any{primitive.AuthoringExtension: map[string]any{"version": 1, "providerIdentity": "fixture", "transport": "tools/call"}}})
			}
			result = map[string]any{"tools": tools, "cacheScope": "private", "ttlMs": 0}
		case "tools/call":
			mu.Lock()
			namespaceReads[token]++
			mu.Unlock()
			var value any
			if request.Params.Name == "namespaces/list" {
				value = map[string]any{"providerIdentity": "fixture", "namespaces": []any{map[string]any{"name": "example", "kinds": []string{}}}, "complete": true}
			} else {
				value = map[string]any{"providerIdentity": "fixture", "namespace": "example", "kinds": []any{}}
			}
			raw, _ := json.Marshal(value)
			result = map[string]any{"content": []any{map[string]any{"type": "text", "text": string(raw)}}}
		default:
			w.WriteHeader(202)
			return
		}
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result}))
	}))
	defer source.Close()
	provider := &warmHTTPProvider{options: &config.MCPClient{ToolsListVisibility: config.ToolsListVisibilityPrivate, ResourceCacheScopes: map[string]string{"*": config.ResourceCacheUser}, ClientOptions: &mcp.ClientOptions{ProtocolVersion: "2025-11-25", Auth: &mcp.ClientAuth{}, Transport: mcp.ClientTransport{Type: "streamable", ClientTransportHTTP: mcp.ClientTransportHTTP{URL: source.URL}}}}}
	owner, err := manager.New(provider)
	require.NoError(t, err)
	defer owner.CloseConversation("")
	registry, err := toolregistry.NewWithManager(owner)
	require.NoError(t, err)
	actor := func(ctx context.Context) (identity.VerifiedActor, error) {
		return identity.VerifiedActor{Issuer: "https://fixture.invalid", Subject: authctx.EffectiveUserID(ctx), TenantID: "tenant", AccountID: "account", IdentityRevision: "1", ValidUntil: time.Now().Add(time.Hour)}, nil
	}
	gateway := resources.NewGateway(owner, actor, func(ctx context.Context, a identity.VerifiedActor) error {
		if a.Subject != authctx.EffectiveUserID(ctx) {
			return identity.ErrResourceDenied
		}
		return nil
	}, "gateway")
	defer gateway.Close()
	lifetime, cancel := context.WithCancel(context.Background())
	defer cancel()
	completed := make(chan error, 2)
	gatewayWarmup := warmGatewayFunc(func(ctx context.Context, name string) ([]primitive.NamespaceCapabilities, error) {
		require.True(t, authconfig.NoRetry(ctx))
		result, err := gateway.DiscoverConnection(ctx, name)
		completed <- err
		return result, err
	})
	handler := withAuthenticatedToolCatalogWarmup(lifetime, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }), registry, owner, ToolCatalogWarmOptions{}, gatewayWarmup)
	for _, subject := range []string{"alice", "bob", "carol"} {
		token := subject + "-token"
		if subject == "carol" {
			token = "alice-token"
		}
		mu.Lock()
		previousLists, previousReads := lists["Bearer "+token], namespaceReads["Bearer "+token]
		mu.Unlock()
		ctx := authctx.WithBearer(authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: subject}), token)
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/v1/events", nil).WithContext(ctx))
		select {
		case err := <-completed:
			require.NoError(t, err)
		case <-time.After(3 * time.Second):
			t.Fatal("authenticated warmup did not derive gateway capabilities")
		}
		mu.Lock()
		count := lists["Bearer "+token]
		reads := namespaceReads["Bearer "+token]
		mu.Unlock()
		require.Equal(t, previousLists+1, count, "zero-TTL tool page must be reused by Gateway within the warmup operation")
		require.Equal(t, previousReads+2, reads, "each principal must derive its own namespace capabilities even when tokens match")
		discovered, err := gateway.Discover(ctx)
		require.NoError(t, err)
		require.Len(t, discovered, 1)
		mu.Lock()
		require.Equal(t, count, lists["Bearer "+token])
		require.Equal(t, reads, namespaceReads["Bearer "+token])
		mu.Unlock()
	}
	// An independent tool listing still refetches the source's zero-TTL page.
	ctx := authctx.WithBearer(authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"}), "alice-token")
	require.NoError(t, registry.WarmToolCatalogs(manager.WithToolCatalogReadSnapshot(ctx), []string{"remote"}))
	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, 3, lists["Bearer alice-token"])
	require.Equal(t, 1, lists["Bearer bob-token"])
}

func TestAuthorizedWarmupCancellationBetweenConsumersSkipsGateway(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started, finished := make(chan struct{}), make(chan struct{})
	go func() {
		warmAuthorizedCatalogs(ctx, warmToolFunc(func(ctx context.Context, _ []string) error {
			close(started)
			<-ctx.Done()
			return nil
		}), []string{"canceling"}, warmGatewayFunc(func(context.Context, string) ([]primitive.NamespaceCapabilities, error) {
			t.Error("gateway dispatched after lifetime cancellation")
			return nil, nil
		}))
		close(finished)
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("tool warmup did not start")
	}
	cancel()
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("canceled warmup did not finish")
	}
}
