package tool

import (
	"context"
	"testing"

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

func TestPrivateSurfaceIdentityTracksPrincipalCredentialsAndExpiry(t *testing.T) {
	m := &discoveryManagerStub{options: &config.MCPClient{ToolsListVisibility: config.ToolsListVisibilityPrivate}}
	r := &Registry{mgr: m, cache: map[string]*toolCacheEntry{}}
	r.storePrincipalDiscoveryTools("private", "alice", "a", false, []schema.Tool{{Name: "secret"}})
	ctx := authctx.WithBearer(authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"}), "a")
	kind, generation := r.ToolSurfaceCacheIdentity(ctx, "private:*")
	require.Equal(t, "private", kind)
	require.NotEmpty(t, generation)
	_, other := r.ToolSurfaceCacheIdentity(authctx.WithBearer(ctx, "new"), "private:*")
	require.Empty(t, other)
	_, other = r.ToolSurfaceCacheIdentity(authctx.WithUserInfo(ctx, &authctx.UserInfo{Subject: "bob"}), "private:*")
	require.Empty(t, other)
	r.storePrincipalDiscoveryTools("private", "bob", "a", false, []schema.Tool{{Name: "secret"}})
	_, other = r.ToolSurfaceCacheIdentity(authctx.WithUserInfo(ctx, &authctx.UserInfo{Subject: "bob"}), "private:*")
	require.NotEmpty(t, other)
	require.NotEqual(t, generation, other)
}
