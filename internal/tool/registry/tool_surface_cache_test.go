package tool

import (
	"context"
	"encoding/json"
	"github.com/viant/agently-core/protocol/mcp/manager"
	"github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/mcp"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	authctx "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/protocol/mcp/config"
	schema "github.com/viant/mcp-protocol/schema"
)

func TestPublicSurfaceIdentityIsSharedAndTracksCatalogAndVisibility(t *testing.T) {
	m := &discoveryManagerStub{options: &config.MCPClient{ToolsListVisibility: config.ToolsListVisibilityPublic}}
	r := &Registry{mgr: m, cache: map[string]*toolCacheEntry{}}
	r.mergeServerTools("public", []schema.Tool{{Name: "read"}})
	_, first := r.ToolSurfaceCacheIdentity(context.Background(), "public:*")
	_, second := r.ToolSurfaceCacheIdentity(authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "bob"}), "public:*")
	require.NotEmpty(t, first)
	require.Equal(t, first, second)
	r.mergeServerTools("public", []schema.Tool{{Name: "new"}})
	_, changed := r.ToolSurfaceCacheIdentity(context.Background(), "public:*")
	require.NotEqual(t, first, changed)
	m.options.ToolsListVisibility = config.ToolsListVisibilityPrivate
	kind, generation := r.ToolSurfaceCacheIdentity(context.Background(), "public:*")
	require.Equal(t, "private", kind)
	require.Empty(t, generation)
}

type surfaceCatalogProvider struct{ options *config.MCPClient }

func (p *surfaceCatalogProvider) Options(context.Context, string) (*config.MCPClient, error) {
	return p.options, nil
}

func TestPrivateSurfaceIdentityTracksPrincipalCredentialsAndInvalidation(t *testing.T) {
	t.Setenv("AGENTLY_MCP_SERVERS", "private")
	var calls, initializes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			w.WriteHeader(405)
			return
		}
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		require.NoError(t, json.NewDecoder(req.Body).Decode(&request))
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Mcp-Session-Id", "surface-session")
		var result any
		switch request.Method {
		case "initialize":
			initializes.Add(1)
			result = map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "surface", "version": "1"}}
		case "tools/list":
			calls.Add(1)
			result = map[string]any{"tools": []any{map[string]any{"name": "secret", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"secret": map[string]any{"type": "string"}}}}}, "cacheScope": "private", "ttlMs": 1000}
		default:
			w.WriteHeader(202)
			return
		}
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result}))
	}))
	defer server.Close()
	provider := &surfaceCatalogProvider{options: &config.MCPClient{ToolsListVisibility: config.ToolsListVisibilityPrivate, ClientOptions: &mcp.ClientOptions{ProtocolVersion: "2025-11-25", Transport: mcp.ClientTransport{Type: "streamable", ClientTransportHTTP: mcp.ClientTransportHTTP{URL: server.URL}}}}}
	owner, err := manager.New(provider)
	require.NoError(t, err)
	r := &Registry{mgr: owner, cache: map[string]*toolCacheEntry{}}
	ctx := requestctx.WithConversationID(authctx.WithBearer(authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"}), "a"), "surface-conversation")
	// Resource gateway metadata already owns this user's shared transport.
	_, err = owner.Get(ctx, "", "private")
	require.NoError(t, err)
	defer owner.CloseConversation("")
	require.NoError(t, r.WarmToolCatalogs(context.Background(), []string{"private"}))
	require.Zero(t, calls.Load(), "anonymous requests must not warm private user catalogs")
	require.NoError(t, r.WarmToolCatalogs(ctx, []string{"private", "private"}))
	require.NoError(t, r.WarmToolCatalogs(ctx, []string{"private"}))
	require.EqualValues(t, 1, calls.Load(), "first-user warmup reuses manager singleflight/cache")
	_, err = r.listServerTools(ctx, "private")
	require.NoError(t, err)
	// Exercise the actual aggregate listing with the real manager and HTTP
	// transport: repeated authoring must reuse the same source catalog.
	require.Len(t, r.DefinitionsWithContext(ctx), 1)
	require.Len(t, r.DefinitionsWithContext(ctx), 1)
	require.EqualValues(t, 1, calls.Load(), "warm aggregate listing must not repeat remote tools/list")
	kind, generation := r.ToolSurfaceCacheIdentity(ctx, "private:*")
	require.Equal(t, "private", kind)
	require.NotEmpty(t, generation)
	promoted := r.promoteLegacyBodyArgs(ctx, "private", "secret", map[string]interface{}{"Body": map[string]interface{}{"secret": "owned"}})
	require.Equal(t, "owned", promoted["secret"], "legacy schema must come from the same manager catalog")
	_, repeated := r.ToolSurfaceCacheIdentity(ctx, "private:*")
	require.Equal(t, generation, repeated)
	require.EqualValues(t, 1, calls.Load(), "identity lookup must reuse manager metadata")
	anotherConversation := requestctx.WithConversationID(ctx, "another-conversation")
	_, err = r.listServerTools(anotherConversation, "private")
	require.NoError(t, err)
	_, reused := r.ToolSurfaceCacheIdentity(anotherConversation, "private:*")
	require.Equal(t, generation, reused)
	require.EqualValues(t, 1, calls.Load(), "same-user conversations share the authenticated HTTP metadata catalog")
	require.EqualValues(t, 1, initializes.Load(), "registry discovery must reuse the resource gateway transport")

	changed := authctx.WithBearer(ctx, "new")
	_, other := r.ToolSurfaceCacheIdentity(changed, "private:*")
	require.Empty(t, other)
	unpromoted := r.promoteLegacyBodyArgs(changed, "private", "secret", map[string]interface{}{"Body": map[string]interface{}{"secret": "other"}})
	require.Contains(t, unpromoted, "Body", "a rotated credential must not reuse the previous schema")
	_, err = r.listServerTools(changed, "private")
	require.NoError(t, err)
	_, other = r.ToolSurfaceCacheIdentity(changed, "private:*")
	require.NotEmpty(t, other)
	require.NotEqual(t, generation, other)
	bob := authctx.WithUserInfo(ctx, &authctx.UserInfo{Subject: "bob"})
	_, other = r.ToolSurfaceCacheIdentity(bob, "private:*")
	require.Empty(t, other)
	_, err = r.listServerTools(bob, "private")
	require.NoError(t, err)
	_, other = r.ToolSurfaceCacheIdentity(bob, "private:*")
	require.NotEmpty(t, other)
	require.NotEqual(t, generation, other)
	require.Empty(t, r.cache, "private catalogs must remain manager owned")
	_, err = owner.Reconnect(ctx, r.metadataConnectionScope(ctx, "surface-conversation", "private"), "private")
	require.NoError(t, err)
	_, other = r.ToolSurfaceCacheIdentity(ctx, "private:*")
	require.Empty(t, other)
	_, err = r.listServerTools(ctx, "private")
	require.NoError(t, err)
	_, other = r.ToolSurfaceCacheIdentity(ctx, "private:*")
	require.NotEmpty(t, other)
	require.NotEqual(t, generation, other)
	retained := other
	previousCalls := calls.Load()
	time.Sleep(1100 * time.Millisecond)
	_, other = r.ToolSurfaceCacheIdentity(ctx, "private:*")
	require.Equal(t, retained, other, "application-held metadata must survive producer TTL")
	require.Len(t, r.DefinitionsWithContext(ctx), 1)
	require.Equal(t, previousCalls, calls.Load(), "ordinary discovery must not revalidate upstream")
}
