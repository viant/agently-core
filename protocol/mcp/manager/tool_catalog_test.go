package manager

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	authctx "github.com/viant/agently-core/internal/auth"
	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/jsonrpc"
	"github.com/viant/mcp"
	"github.com/viant/mcp-protocol/schema"
	mcpclient "github.com/viant/mcp/client"
	authcfg "github.com/viant/mcp/client/auth/config"
)

type catalogProvider struct{ cfg *mcpcfg.MCPClient }

func (p *catalogProvider) Options(context.Context, string) (*mcpcfg.MCPClient, error) {
	return p.cfg, nil
}

type catalogClient struct {
	stubClient
	calls atomic.Int32
}

func (c *catalogClient) ListTools(ctx context.Context, cursor *string, _ ...mcpclient.RequestOption) (*schema.ListToolsResult, error) {
	c.calls.Add(1)
	name := "generic/tool"
	result := &schema.ListToolsResult{Tools: []schema.Tool{{Name: name, Meta: map[string]interface{}{"custom": map[string]interface{}{"nested": "original"}}}}}
	if cursor == nil {
		next := "page2"
		result.NextCursor = &next
	} else {
		result.Tools[0].Name = *cursor
	}
	return result, nil
}
func catalogSetup(t *testing.T, public bool) (*Manager, *catalogProvider, *catalogClient, *managedClient, *managedClient) {
	t.Helper()
	cfg := &mcpcfg.MCPClient{ClientOptions: &mcp.ClientOptions{Transport: mcp.ClientTransport{Type: "streamable", ClientTransportHTTP: mcp.ClientTransportHTTP{URL: "https://example.test/mcp"}}}}
	if public {
		cfg.ToolsListVisibility = "public"
	}
	p := &catalogProvider{cfg: cfg}
	m, err := New(p)
	if err != nil {
		t.Fatal(err)
	}
	client := &catalogClient{}
	// Test the managed boundary without an unknown custom transport factory.
	a := &managedClient{mgr: m, key: "alice:session", serverName: "server", entry: &entry{client: client}}
	b := &managedClient{mgr: m, key: "bob:session", serverName: "server", entry: &entry{client: client}}
	return m, p, client, a, b
}
func TestToolCatalogPublicSharesPagesAndCopiesMetadata(t *testing.T) {
	_, _, client, a, b := catalogSetup(t, true)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c := a
			if i%2 != 0 {
				c = b
			}
			if _, err := c.ListTools(context.Background(), nil); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	if n := client.calls.Load(); n != 1 {
		t.Fatalf("shared first page calls=%d", n)
	}
	result, _ := a.ListTools(context.Background(), nil)
	result.Tools[0].Meta["custom"].(map[string]interface{})["nested"] = "mutated"
	other, _ := b.ListTools(context.Background(), nil)
	if other.Tools[0].Meta["custom"].(map[string]interface{})["nested"] != "original" {
		t.Fatal("shared metadata mutated")
	}
	next := *other.NextCursor
	page, err := b.ListTools(context.Background(), &next)
	if err != nil || page.Tools[0].Name != "page2" {
		t.Fatalf("pagination: %v %v", page, err)
	}
	_, _ = a.ListTools(context.Background(), &next)
	if n := client.calls.Load(); n != 2 {
		t.Fatalf("page cache calls=%d", n)
	}
}
func TestToolCatalogPrivateSeparatesSessionsAndAuthContext(t *testing.T) {
	_, _, client, a, b := catalogSetup(t, false)
	alice := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
	_, _ = a.ListTools(alice, nil)
	_, _ = a.ListTools(alice, nil)
	_, _ = b.ListTools(alice, nil)
	_, _ = a.ListTools(authctx.WithBearer(alice, "different"), nil)
	_, _ = a.ListTools(alice, nil, mcpclient.WithAuthToken("override"))
	if n := client.calls.Load(); n != 4 {
		t.Fatalf("private scopes calls=%d", n)
	}
}
func TestToolCatalogConfigNotificationsAndReconnect(t *testing.T) {
	m, p, client, a, _ := catalogSetup(t, true)
	_, _ = a.ListTools(context.Background(), nil)
	p.cfg.Transport.URL = "https://other.test/mcp"
	_, _ = a.ListTools(context.Background(), nil)
	p.cfg.ToolsListVisibility = "private"
	_, _ = a.ListTools(context.Background(), nil)
	h := &changeHandler{manager: m, server: "server"}
	h.OnNotification(context.Background(), &jsonrpc.Notification{Method: "notifications/tools/list_changed"})
	_, _ = a.ListTools(context.Background(), nil)
	m.newClientFn = func(context.Context, string, string) (mcpclient.Interface, error) { return client, nil }
	_, err := m.Reconnect(context.Background(), "session", "server")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = a.ListTools(context.Background(), nil)
	if n := client.calls.Load(); n != 5 {
		t.Fatal(fmt.Sprintf("configuration/generation calls=%d", n))
	}
}
func TestToolCatalogConfiguredAuthNeverSharesPublic(t *testing.T) {
	_, p, client, a, b := catalogSetup(t, true)
	p.cfg.Auth = &mcp.ClientAuth{}
	_, _ = a.ListTools(context.Background(), nil)
	_, _ = b.ListTools(context.Background(), nil)
	if client.calls.Load() != 2 {
		t.Fatal("authenticated catalogs shared")
	}
}

func TestToolCatalogThroughHTTPManager(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Mcp-Session-Id", "catalog-session")
		var result any
		switch request.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "catalog", "version": "1"}}
		case "tools/list":
			calls.Add(1)
			result = map[string]any{"tools": []any{map[string]any{"name": "generic/tool", "inputSchema": map[string]any{"type": "object"}}}, "cacheScope": "public", "resultType": "complete", "ttlMs": 60000}
		default:
			w.WriteHeader(202)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
	}))
	defer server.Close()
	p := &catalogProvider{cfg: &mcpcfg.MCPClient{ToolsListVisibility: "public", ClientOptions: &mcp.ClientOptions{ProtocolVersion: "2025-11-25", Transport: mcp.ClientTransport{Type: "streamable", ClientTransportHTTP: mcp.ClientTransportHTTP{URL: server.URL}}}}}
	m, err := New(p)
	if err != nil {
		t.Fatal(err)
	}
	alice := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
	bob := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "bob"})
	for _, ctx := range []context.Context{alice, bob} {
		cli, err := m.Get(ctx, "same-session", "server")
		if err != nil {
			t.Fatal(err)
		}
		tools, err := cli.ListTools(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(tools.Tools) != 1 {
			t.Fatal("missing catalog")
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("public HTTP calls=%d", calls.Load())
	}
	p.cfg.ToolsListVisibility = "private"
	for _, ctx := range []context.Context{alice, bob} {
		cli, err := m.Get(ctx, "same-session", "server")
		if err != nil {
			t.Fatal(err)
		}
		_, err = cli.ListTools(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		_, err = cli.ListTools(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 3 {
		t.Fatalf("protected HTTP calls=%d", calls.Load())
	}
	cli, err := m.Get(alice, "same-session", "server")
	if err != nil {
		t.Fatal(err)
	}
	_, err = cli.ListTools(alice, nil, mcpclient.WithAuthToken("rotated-credential"))
	if err != nil {
		t.Fatal(err)
	}
	sameUserOtherSession, err := m.Get(alice, "another-session", "server")
	if err != nil {
		t.Fatal(err)
	}
	_, err = sameUserOtherSession.ListTools(alice, nil)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 5 {
		t.Fatalf("rotation/session HTTP calls=%d", calls.Load())
	}

	resolver := &rotatingCatalogResolver{token: "delegated-a"}
	m.credResolver = resolver
	p.cfg.Auth = &mcp.ClientAuth{ExternalResolver: resolver}
	delegated, err := m.Get(alice, "delegated-session", "server")
	if err != nil {
		t.Fatal(err)
	}
	_, err = delegated.ListTools(alice, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = delegated.ListTools(alice, nil)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 6 {
		t.Fatalf("delegated cache HTTP calls=%d", calls.Load())
	}
	resolver.token = "delegated-b"
	_, err = delegated.ListTools(alice, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = delegated.ListTools(alice, nil)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 7 {
		t.Fatalf("delegated rotation HTTP calls=%d", calls.Load())
	}

}

type rotatingCatalogResolver struct {
	staticCredentialResolver
	token string
}

func (r *rotatingCatalogResolver) Resolve(context.Context, authcfg.Requirement) (*authcfg.Credential, error) {
	return &authcfg.Credential{Token: r.token}, nil
}
