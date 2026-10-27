package resource

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	authctx "github.com/viant/agently-core/internal/auth"
	cfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcp/manager"
	identity "github.com/viant/agently-core/protocol/resource"
	fsstore "github.com/viant/agently-core/workspace/store/fs"
	"github.com/viant/mcp"
	"github.com/viant/mcp-protocol/schema"
	client "github.com/viant/mcp/client"
)

func TestOperatorLocalMCPOverridesPublishedNameAndCanonicalServiceURI(t *testing.T) {
	actor, verify := PublicReadActors()
	internal := &referenceCatalog{namespace: "workspace", owner: "configuration", kind: "mcp", rows: map[string]map[string]json.RawMessage{}, denied: map[string]bool{}}
	remote := &referenceCatalog{namespace: "ar", owner: "studio-owner", kind: "mcp", rows: map[string]map[string]json.RawMessage{}, denied: map[string]bool{}}
	localEndpoint := &referenceCatalog{namespace: "local-executable", owner: "local-executable-owner", kind: "mcp", rows: map[string]map[string]json.RawMessage{}, denied: map[string]bool{}}
	internal.set("", "studio", referenceConfig("studio", "studio-owner", cfg.ResourceCacheUser))
	remoteConfig := referenceConfig("remote-sdk-name", "", cfg.ResourceCacheUser)
	remote.set("alice", "ar", remoteConfig)
	remote.set("alice", "unrelated", referenceConfig("unrelated", "", cfg.ResourceCacheUser))
	var mu sync.Mutex
	opened := map[string]int{}
	mgr, err := manager.New(manager.NewRepoProvider(manager.WithRepoStateStore(fsstore.NewStateStore(t.TempDir()))), manager.WithClientFactory(func(_ context.Context, _, name string) (client.Interface, error) {
		mu.Lock()
		opened[name]++
		mu.Unlock()
		switch name {
		case "ar":
			return referenceClient(t, localEndpoint)
		case "studio", "mcp://ar/ar", "mcp://ar/unrelated":
			return referenceClient(t, remote)
		default:
			return nil, fmt.Errorf("unexpected transport %s", name)
		}
	}))
	require.NoError(t, err)
	t.Cleanup(func() { mgr.CloseConversation(""); mgr.CloseConversation("caller") })
	require.NoError(t, mgr.RegisterLocal(context.Background(), "configuration", &cfg.MCPClient{ClientOptions: &mcp.ClientOptions{}, PrimitiveProviderIdentity: "configuration", ResourceCacheScopes: map[string]string{"workspace": cfg.ResourceCachePublic}}, func(context.Context) (client.Interface, error) { return referenceClient(t, internal) }))
	gateway := NewGateway(mgr, actor, verify, "consumer")
	t.Cleanup(gateway.Close)
	reader, err := NewMCPReferenceReader(gateway, MCPDefinitionSource{ConnectionName: "configuration", ProviderIdentity: "configuration", Namespace: "workspace"}, []identity.ResourceRef{{URI: "mcp://workspace/studio"}})
	require.NoError(t, err)
	bound, err := mgr.BindDefinitionReader(reader)
	require.NoError(t, err)
	require.True(t, bound)
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
	// First warm the published definition and its client before an operator adds
	// the override, so a remote memo or transport cannot conceal local precedence.
	original, err := reader.Load(ctx, "mcp://ar/ar")
	require.NoError(t, err)
	require.Equal(t, remoteConfig.Transport.URL, original.Transport.URL)
	oldClient, err := mgr.Get(ctx, "caller", "mcp://ar/ar")
	require.NoError(t, err)
	remotePin, err := Get[*cfg.MCPClient](ctx, gateway, Connection{Name: "studio", ProviderIdentity: "studio-owner"}, identity.ResourceRef{URI: "mcp://ar/ar"}, nil, func(raw json.RawMessage) (*cfg.MCPClient, error) { return DecodeMCPDefinition(1, raw) })
	require.NoError(t, err)
	local := referenceConfig("different-local-sdk-display-name", "", cfg.ResourceCachePublic)
	internal.set("", "ar", local)
	internal.changes.Publish(ctx)
	localNames, err := reader.CatalogNames(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"studio"}, localNames, "an ordinary local override must not become another discovery root")
	for _, name := range []string{"ar", "mcp://ar/ar", "mcp://workspace/ar"} {
		selected, err := reader.Load(ctx, name)
		require.NoError(t, err)
		require.Equal(t, local.Transport.URL, selected.Transport.URL, name)
		require.Equal(t, &cfg.ResolvedServiceIdentity{ServiceName: "ar", ResourceURI: "mcp://workspace/ar", ProviderIdentity: "configuration"}, selected.ResolvedIdentity, "local bytes retain their actual owning identity")
		options, err := mgr.Options(ctx, name)
		require.NoError(t, err)
		require.Equal(t, local.Transport.URL, options.Transport.URL)
	}
	names, err := mgr.Names(ctx)
	require.NoError(t, err)
	require.Contains(t, names, "ar")
	require.NotContains(t, names, "mcp://ar/ar")
	require.Contains(t, names, "mcp://ar/unrelated")
	localClient, err := mgr.Get(ctx, "caller", "ar")
	require.NoError(t, err)
	canonicalClient, err := mgr.Get(ctx, "caller", "mcp://ar/ar")
	require.NoError(t, err)
	require.Same(t, localClient, canonicalClient)
	require.NotSame(t, oldClient, canonicalClient, "a warm published transport cannot override the changed effective local definition")
	response, err := canonicalClient.CallTool(ctx, &schema.CallToolRequestParams{Name: "namespaces/list", Arguments: map[string]interface{}{}}, client.WithNoRetry())
	require.NoError(t, err)
	wire, err := json.Marshal(response)
	require.NoError(t, err)
	require.Contains(t, string(wire), "local-executable", "the canonical service call executes on the selected local transport")
	_, err = mgr.Get(ctx, "caller", "mcp://ar/unrelated")
	require.NoError(t, err, "unrelated published definitions remain executable")
	mu.Lock()
	require.Equal(t, 1, opened["ar"])
	require.Equal(t, 1, opened["mcp://ar/ar"])
	mu.Unlock()
	// Remote publication changes cannot replace the operator's transport. Exact
	// resource reads and held pins still concern the real published definition.
	remote.set("alice", "ar", referenceConfig("changed-remote", "", cfg.ResourceCacheUser))
	remote.changes.Publish(ctx)
	after, err := mgr.Get(ctx, "caller", "mcp://ar/ar")
	require.NoError(t, err)
	require.Same(t, localClient, after)
	_, err = Get[*cfg.MCPClient](ctx, gateway, Connection{Name: "studio", ProviderIdentity: "studio-owner"}, identity.ResourceRef{URI: "mcp://ar/ar"}, remotePin.ResolvedResource, func(raw json.RawMessage) (*cfg.MCPClient, error) { return DecodeMCPDefinition(1, raw) })
	require.Error(t, err, "a local routing override cannot forge or renew a remote resource pin")
	_, err = reader.Load(ctx, "configuration")
	require.ErrorIs(t, err, ErrCollision, "intrinsic in-process service names remain protected")
}
