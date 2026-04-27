package auth

import (
	"context"
	"time"

	token "github.com/viant/agently-core/internal/auth/token"
)

type canonicalTokenStore struct {
	inner TokenStore
	users UserService
}

func (s *canonicalTokenStore) resolveOwner(ctx context.Context, username, provider string) string {
	if s == nil || s.inner == nil {
		return ""
	}
	sess := &Session{
		Username: username,
		Subject:  username,
		Provider: provider,
	}
	return resolveOAuthTokenOwnerID(ctx, s.users, provider, sess)
}

func (s *canonicalTokenStore) Get(ctx context.Context, username, provider string) (*OAuthToken, error) {
	return s.inner.Get(ctx, s.resolveOwner(ctx, username, provider), provider)
}

func (s *canonicalTokenStore) Put(ctx context.Context, token *OAuthToken) error {
	if token == nil {
		return nil
	}
	next := *token
	next.Username = s.resolveOwner(ctx, token.Username, token.Provider)
	return s.inner.Put(ctx, &next)
}

func (s *canonicalTokenStore) Delete(ctx context.Context, username, provider string) error {
	return s.inner.Delete(ctx, s.resolveOwner(ctx, username, provider), provider)
}

func (s *canonicalTokenStore) TryAcquireRefreshLease(ctx context.Context, username, provider, owner string, ttl time.Duration) (int64, bool, error) {
	return s.inner.TryAcquireRefreshLease(ctx, s.resolveOwner(ctx, username, provider), provider, owner, ttl)
}

func (s *canonicalTokenStore) ReleaseRefreshLease(ctx context.Context, username, provider, owner string) error {
	return s.inner.ReleaseRefreshLease(ctx, s.resolveOwner(ctx, username, provider), provider, owner)
}

func (s *canonicalTokenStore) CASPut(ctx context.Context, token *OAuthToken, expectedVersion int64, owner string) (bool, error) {
	if token == nil {
		return false, nil
	}
	next := *token
	next.Username = s.resolveOwner(ctx, token.Username, token.Provider)
	return s.inner.CASPut(ctx, &next, expectedVersion, owner)
}

// tokenStoreAdapter bridges service/auth.TokenStore to token.TokenStore.
// This is needed because the two packages define mirrored types to avoid import cycles.
type tokenStoreAdapter struct {
	inner TokenStore
}

// NewTokenStoreAdapter wraps a service/auth.TokenStore to satisfy token.TokenStore.
func NewTokenStoreAdapter(store TokenStore, users UserService) token.TokenStore {
	return &tokenStoreAdapter{inner: &canonicalTokenStore{inner: store, users: users}}
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
