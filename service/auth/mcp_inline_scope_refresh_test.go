package auth

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	iauth "github.com/viant/agently-core/internal/auth"
	"golang.org/x/oauth2"
)

func TestBackgroundRefreshUsesValidatedStoredInlineOwnerWithoutFabricatedIdentity(t *testing.T) {
	store := newFakeDelegatedStore()
	resolver := newTestResolver(t, store)
	resolver.users = &fakeUserLookup{users: map[string]*User{"uuid-1": {ID: "uuid-1"}, "uuid-2": {ID: "uuid-2"}}}
	inline := inlineDev6Provider()
	require.NoError(t, resolver.registry.RegisterInline(delegatedCtx("uuid-1"), inline))
	now := time.Now()
	row := &OAuthToken{Username: "uuid-1", Provider: DelegatedProviderStorageKey("test-ns", inline.ID), ProviderRef: inline.ID, ClientRef: inline.DefaultClient, Issuer: inline.Issuer, Resource: "https://mcp-inline.example.com/mcp", Scopes: []string{"plan:read"}, TokenType: "accessToken", AccessToken: "synthetic-old", RefreshToken: "synthetic-refresh", IssuedAt: now.Add(-55 * time.Minute), ExpiresAt: now.Add(5 * time.Minute)}
	store.seed(row)
	calls := 0
	resolver.refreshToken = func(ctx context.Context, _ *oauth2.Config, _ *oauth2.Token, _ []string, _ string) (*oauth2.Token, error) {
		require.Empty(t, iauth.CanonicalUserID(ctx))
		require.Empty(t, iauth.EffectiveUserID(ctx))
		require.Empty(t, iauth.Provider(ctx), "background lookup must not fabricate authenticated ingress")
		calls++
		return &oauth2.Token{AccessToken: "synthetic-fresh", Expiry: now.Add(time.Hour)}, nil
	}
	require.NoError(t, resolver.RefreshStoredDelegatedToken(context.Background(), row))
	require.Equal(t, 1, calls)
	require.Error(t, resolver.RefreshStoredDelegatedToken(delegatedCtx("uuid-2"), row))
	require.Equal(t, 1, calls, "foreign authenticated caller cannot adopt stored owner lookup")
	foreign := *row
	foreign.Username = "uuid-2"
	require.Error(t, resolver.RefreshStoredDelegatedToken(context.Background(), &foreign))
	require.Equal(t, 1, calls, "another stored owner's provider must not be visible")
	resolver.users = &fakeUserLookup{users: map[string]*User{"uuid-1": {ID: "uuid-1", Disabled: true}}}
	require.Error(t, resolver.RefreshStoredDelegatedToken(context.Background(), row))
	require.Equal(t, 1, calls, "disabled stored owner cannot refresh")
}
