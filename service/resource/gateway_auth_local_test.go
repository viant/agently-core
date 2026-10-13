package resource

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	authctx "github.com/viant/agently-core/internal/auth"
	tokenstore "github.com/viant/agently-core/internal/auth/token"
	cfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcp/manager"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/mcp"
	mcpclient "github.com/viant/mcp/client"
	scyauth "github.com/viant/scy/auth"
	"golang.org/x/oauth2"
)

func TestGatewayLocalCallsPreserveOriginalIdentityWithSavedOAuthTokens(t *testing.T) {
	provider, _, actor, _ := localFixture(t)
	original := "current-verified-request"
	cached := "different-saved-oauth"
	tokens := tokenstore.NewManager()
	require.NoError(t, tokens.Store(context.Background(), tokenstore.Key{Subject: "canonical-owned-user", Provider: "oauth"}, &scyauth.Token{Token: oauth2.Token{AccessToken: "owned-access", Expiry: time.Now().Add(time.Hour)}, IDToken: cached}))
	ctx := authctx.WithUserInfo(authctx.WithIDToken(context.Background(), original), &authctx.UserInfo{Subject: "canonical-owned-user"})
	// The actual saved-token manager would replace a JWT request without an
	// OAuth token expiry; this must never run across an in-process authority hop.
	rewritten, err := tokens.EnsureTokens(ctx, tokenstore.Key{Subject: "canonical-owned-user", Provider: "oauth"})
	require.NoError(t, err)
	require.Equal(t, cached, authctx.IDToken(rewritten))
	resolve := func(ctx context.Context) (identity.VerifiedActor, error) {
		if authctx.IDToken(ctx) != original {
			return identity.VerifiedActor{}, identity.ErrResourceDenied
		}
		return *actor, nil
	}
	verify := func(ctx context.Context, held identity.VerifiedActor) error {
		current, err := resolve(ctx)
		if err != nil {
			return err
		}
		if current.IdentityRevision != held.IdentityRevision || !held.Valid(time.Now()) {
			return identity.ErrResourceDenied
		}
		return nil
	}
	updateLocalProvider(t, provider, func(config *LocalConfig) {
		config.Actor = resolve
	})
	updateLocalProvider(t, provider, func(config *LocalConfig) {
		config.Verify = verify
	})
	mgr, err := manager.New(nil, manager.WithTokenProvider(tokens))
	require.NoError(t, err)
	// An arbitrary registered name proves the distinction comes from manager
	// registration, not a special-case string such as "internal".
	require.NoError(t, mgr.RegisterLocal(ctx, "owned-framework", &cfg.MCPClient{ClientOptions: &mcp.ClientOptions{}, PrimitiveProviderIdentity: provider.ProviderIdentity()}, func(context.Context) (mcpclient.Interface, error) { return NewLocalMCPClient(provider) }))
	gateway := NewGateway(mgr, resolve, verify, "gateway")
	defer gateway.Close()
	rows, err := gateway.List(ctx, "window", "team")
	require.NoError(t, err, "local catalog must retain original verified request context")
	require.Len(t, rows, 1)
	_, err = gateway.Get(ctx, rows[0].Connection, identity.ResourceRef{URI: rows[0].Resource.URI}, nil)
	require.NoError(t, err)
}
