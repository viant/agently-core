package auth

import (
	"context"
	"time"

	token "github.com/viant/agently-core/internal/auth/token"
)

// tokenStoreAdapter bridges service/auth.TokenStore to token.TokenStore.
// This is needed because the two packages define mirrored types to avoid import cycles.
type tokenStoreAdapter struct {
	inner TokenStore
}

// NewTokenStoreAdapter wraps a service/auth.TokenStore to satisfy token.TokenStore.
func NewTokenStoreAdapter(store TokenStore) token.TokenStore {
	return &tokenStoreAdapter{inner: store}
}

func (a *tokenStoreAdapter) Get(ctx context.Context, username, provider string) (*token.OAuthToken, error) {
	t, err := a.inner.Get(ctx, username, provider)
	if err != nil || t == nil {
		return nil, err
	}
	return &token.OAuthToken{
		Username:     t.Username,
		Provider:     t.Provider,
		AccessToken:  t.AccessToken,
		IDToken:      t.IDToken,
		RefreshToken: t.RefreshToken,
		ExpiresAt:    t.ExpiresAt,
	}, nil
}

func (a *tokenStoreAdapter) Put(ctx context.Context, t *token.OAuthToken) error {
	if t == nil {
		return nil
	}
	return a.inner.Put(ctx, &OAuthToken{
		Username:     t.Username,
		Provider:     t.Provider,
		AccessToken:  t.AccessToken,
		IDToken:      t.IDToken,
		RefreshToken: t.RefreshToken,
		ExpiresAt:    t.ExpiresAt,
	})
}

func (a *tokenStoreAdapter) Delete(ctx context.Context, username, provider string) error {
	return a.inner.Delete(ctx, username, provider)
}

func (a *tokenStoreAdapter) TryAcquireRefreshLease(ctx context.Context, username, provider, owner string, ttl time.Duration) (int64, bool, error) {
	return a.inner.TryAcquireRefreshLease(ctx, username, provider, owner, ttl)
}

func (a *tokenStoreAdapter) ReleaseRefreshLease(ctx context.Context, username, provider, owner string) error {
	return a.inner.ReleaseRefreshLease(ctx, username, provider, owner)
}

func (a *tokenStoreAdapter) CASPut(ctx context.Context, t *token.OAuthToken, expectedVersion int64, owner string) (bool, error) {
	if t == nil {
		return false, nil
	}
	return a.inner.CASPut(ctx, &OAuthToken{
		Username:     t.Username,
		Provider:     t.Provider,
		AccessToken:  t.AccessToken,
		IDToken:      t.IDToken,
		RefreshToken: t.RefreshToken,
		ExpiresAt:    t.ExpiresAt,
	}, expectedVersion, owner)
}
