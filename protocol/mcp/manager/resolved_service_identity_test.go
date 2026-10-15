package manager

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	authctx "github.com/viant/agently-core/internal/auth"
	cfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/mcp"
	mcpclient "github.com/viant/mcp/client"
	authstore "github.com/viant/mcp/client/auth/store"
	"golang.org/x/oauth2"
	"gopkg.in/yaml.v3"
)

func TestResolvedServiceAliasesShareClientTokenStoreAndCallbackIdentity(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGENTLY_WORKSPACE", root)
	t.Setenv("AGENTLY_RUNTIME_ROOT", filepath.Join(root, "state"))
	endpoint, _ := poolConfigEndpoint(t, "remote", nil, nil)
	uri := "mcp://workspace/remote"
	definition := &cfg.MCPClient{ClientOptions: &mcp.ClientOptions{Name: "handshake-only", Version: "1", Transport: mcp.ClientTransport{Type: "streamable", ClientTransportHTTP: mcp.ClientTransportHTTP{URL: endpoint.URL + "/mcp"}}}, ResolvedIdentity: &cfg.ResolvedServiceIdentity{ServiceName: "remote", ResourceURI: uri, ProviderIdentity: "configuration"}}
	reader := &definitionReaderFixture{definitions: map[string]*cfg.MCPClient{"remote": definition, uri: definition}}
	provider := NewRepoProvider(WithRepoDefinitionReader(reader))
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
	canonical, err := provider.Options(ctx, uri)
	require.NoError(t, err)
	key := authstore.TokenKey{Issuer: "fixture", Scopes: "metadata"}
	require.NoError(t, canonical.Auth.Store.AddToken(key, &oauth2.Token{AccessToken: "synthetic-owned-token", Expiry: time.Now().Add(time.Hour)}))
	legacy, err := provider.Options(ctx, "remote")
	require.NoError(t, err)
	token, exists := legacy.Auth.Store.LookupToken(key)
	require.True(t, exists)
	require.Equal(t, "synthetic-owned-token", token.AccessToken, "aliases must use the same configured token-store identity")
	require.Equal(t, "handshake-only", canonical.Name)
	require.NotSame(t, definition.ResolvedIdentity, canonical.ResolvedIdentity)
	canonical.ResolvedIdentity.ServiceName = "caller mutation"
	require.Equal(t, "remote", definition.ResolvedIdentity.ServiceName)
	var createdService string
	mgr, err := New(provider, WithClientFactory(func(_ context.Context, _, service string) (mcpclient.Interface, error) {
		createdService = service
		return &stubClient{}, nil
	}))
	require.NoError(t, err)
	defer mgr.CloseConversation("")
	first, err := mgr.Get(ctx, "", uri)
	require.NoError(t, err)
	second, err := mgr.Get(ctx, "", "remote")
	require.NoError(t, err)
	require.Same(t, first, second)
	require.True(t, mgr.IsCurrentClient(ctx, "", uri, first))
	require.True(t, mgr.IsCurrentClient(ctx, "", "remote", first))
	require.Equal(t, "remote", createdService, "auth and notification callbacks retain configured service identity")
	reader.failure = context.Canceled
	denied, err := mgr.Get(ctx, "", uri)
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, denied)
	require.False(t, mgr.IsCurrentClient(ctx, "", "remote", first), "removal/denial by canonical alias must evict the resolved entry")
}

func TestResolvedServiceIdentityNeverUsesHandshakeNameOrMergesProvenance(t *testing.T) {
	a := &cfg.MCPClient{ClientOptions: &mcp.ClientOptions{Name: "same-handshake"}, ResolvedIdentity: &cfg.ResolvedServiceIdentity{ServiceName: "remote", ResourceURI: "mcp://one/remote", ProviderIdentity: "one"}}
	b := &cfg.MCPClient{ClientOptions: &mcp.ClientOptions{Name: "same-handshake"}, ResolvedIdentity: &cfg.ResolvedServiceIdentity{ServiceName: "remote", ResourceURI: "mcp://two/remote", ProviderIdentity: "two"}}
	provider := NewRepoProvider(WithRepoDefinitionReader(&definitionReaderFixture{definitions: map[string]*cfg.MCPClient{"mcp://one/remote": a, "mcp://two/remote": b}}))
	mgr, err := New(provider, WithClientFactory(func(context.Context, string, string) (mcpclient.Interface, error) { return &stubClient{}, nil }))
	require.NoError(t, err)
	defer mgr.CloseConversation("")
	first, err := mgr.Get(context.Background(), "", "mcp://one/remote")
	require.NoError(t, err)
	second, err := mgr.Get(context.Background(), "", "mcp://two/remote")
	require.NoError(t, err)
	require.NotSame(t, first, second, "same handshake/name cannot merge distinct managing resource identities")
	require.False(t, mgr.IsCurrentClient(context.Background(), "", "mcp://one/remote", first))
	for _, marshal := range []func(any) ([]byte, error){json.Marshal, yaml.Marshal} {
		raw, err := marshal(a)
		require.NoError(t, err)
		require.False(t, strings.Contains(string(raw), "mcp://one/remote"), "trusted source identity cannot be accepted or emitted as authored configuration")
	}
}
