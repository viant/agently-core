package tool

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	authctx "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/mcp"
	schema "github.com/viant/mcp-protocol/schema"
	client "github.com/viant/mcp/client"
)

type snapshotOnlyIdentityManager struct {
	discoveryManagerStub
	snapshots int
}

func (m *snapshotOnlyIdentityManager) ToolCatalogSnapshot(context.Context, string, string, ...client.RequestOption) (string, []schema.Tool, error) {
	m.snapshots++
	return "existing-page-revision", nil, nil
}

type lightweightIdentityManager struct {
	snapshotOnlyIdentityManager
	identities int
	empty      bool
}

func (m *lightweightIdentityManager) ToolCatalogIdentity(ctx context.Context, conversation, server string, options ...client.RequestOption) (string, error) {
	m.identities++
	if server != "private" || authctx.EffectiveUserID(ctx) != "alice" || client.NewRequestOptions(options).StringToken != "credential" {
		return "", nil
	}
	if m.empty {
		return "", nil
	}
	return "existing-page-revision", nil
}
func newSnapshotOnlyIdentityManager() snapshotOnlyIdentityManager {
	return snapshotOnlyIdentityManager{discoveryManagerStub: discoveryManagerStub{options: &config.MCPClient{ToolsListVisibility: config.ToolsListVisibilityPrivate, ClientOptions: &mcp.ClientOptions{Transport: mcp.ClientTransport{Type: "streamable", ClientTransportHTTP: mcp.ClientTransportHTTP{URL: "https://fixture.invalid/mcp"}}}}}}
}
func TestToolSurfaceCacheIdentityUsesLightweightOptionalInterface(t *testing.T) {
	manager := &lightweightIdentityManager{snapshotOnlyIdentityManager: newSnapshotOnlyIdentityManager()}
	registry := &Registry{mgr: manager, cache: map[string]*toolCacheEntry{}}
	ctx := authctx.WithBearer(authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"}), "credential")
	kind, identity := registry.ToolSurfaceCacheIdentity(ctx, "private:*")
	require.Equal(t, "private", kind)
	require.NotEmpty(t, identity)
	require.Equal(t, 1, manager.identities)
	require.Zero(t, manager.snapshots)
	require.Empty(t, manager.getCallsSnapshot())
	manager.empty = true
	_, identity = registry.ToolSurfaceCacheIdentity(ctx, "private:*")
	require.Empty(t, identity)
	require.Zero(t, manager.snapshots, "a read-only miss must not initialize a transport via snapshot fallback")
}
func TestToolSurfaceCacheIdentityRetainsAlternateManagerSnapshotFallback(t *testing.T) {
	manager := newSnapshotOnlyIdentityManager()
	registry := &Registry{mgr: &manager, cache: map[string]*toolCacheEntry{}}
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
	kind, identity := registry.ToolSurfaceCacheIdentity(ctx, "private:*")
	require.Equal(t, "private", kind)
	require.NotEmpty(t, identity)
	require.Equal(t, 1, manager.snapshots)
}
