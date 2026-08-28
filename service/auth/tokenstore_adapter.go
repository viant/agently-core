package auth

import (
	"context"
	"strings"
	"sync"
	"time"

	token "github.com/viant/agently-core/internal/auth/token"
	"github.com/viant/agently-core/internal/authlog"
)

type canonicalTokenStore struct {
	inner TokenStore
	users UserService
	mu    sync.RWMutex
	ids   map[string]string
}

func (s *canonicalTokenStore) resolveOwner(ctx context.Context, username, provider string) string {
	if s == nil || s.inner == nil {
		return ""
	}
	// Delegated storage keys are always addressed by the canonical users.id
	// directly; provider-based owner resolution must never run for them.
	if IsDelegatedProviderKey(provider) {
		return strings.TrimSpace(username)
	}
	cacheKey := strings.TrimSpace(provider) + "|" + strings.TrimSpace(username)
	if cacheKey != "|" {
		s.mu.RLock()
		if resolved, ok := s.ids[cacheKey]; ok && strings.TrimSpace(resolved) != "" {
			s.mu.RUnlock()
			return resolved
		}
		s.mu.RUnlock()
	}
	sess := &Session{
		Username: username,
		Subject:  username,
		Provider: provider,
	}
	resolved := resolveOAuthTokenOwnerID(ctx, s.users, provider, sess)
	if cacheKey != "|" && strings.TrimSpace(resolved) != "" {
		s.mu.Lock()
		if s.ids == nil {
			s.ids = map[string]string{}
		}
		s.ids[cacheKey] = resolved
		s.mu.Unlock()
	}
	return resolved
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
	inner  TokenStore
	client *OAuthClient
}

// NewTokenStoreAdapter wraps a service/auth.TokenStore to satisfy token.TokenStore.
func NewTokenStoreAdapter(store TokenStore, users UserService) token.TokenStore {
	return newTokenStoreAdapter(store, users, nil)
}

func newTokenStoreAdapter(store TokenStore, users UserService, client *OAuthClient) token.TokenStore {
	return &tokenStoreAdapter{
		inner:  &canonicalTokenStore{inner: store, users: users, ids: map[string]string{}},
		client: client,
	}
}

func (a *tokenStoreAdapter) Get(ctx context.Context, username, provider string) (*token.OAuthToken, error) {
	t, err := a.inner.Get(ctx, username, provider)
	if err != nil || t == nil {
		return nil, err
	}
	expiresAt := t.ExpiresAt
	if err := validateConfiguredOAuthScopes(a.client, nil, t.IDToken, t.AccessToken, t.RefreshToken); err != nil {
		authlog.Log(ctx, authlog.Event{
			Op:             "scope_validate_stored",
			UserID:         strings.TrimSpace(t.Username),
			Provider:       strings.TrimSpace(t.Provider),
			Classification: "scope_validation",
			Action:         "preserve_refresh_no_inject",
			Err:            err,
		})
		expiresAt = time.Time{}
	}
	return &token.OAuthToken{
		Username:         t.Username,
		Provider:         t.Provider,
		AccessToken:      t.AccessToken,
		IDToken:          t.IDToken,
		RefreshToken:     t.RefreshToken,
		ExpiresAt:        expiresAt,
		Issuer:           t.Issuer,
		Resource:         t.Resource,
		Scopes:           append([]string(nil), t.Scopes...),
		TokenType:        t.TokenType,
		Subject:          t.Subject,
		ProviderRef:      t.ProviderRef,
		ClientRef:        t.ClientRef,
		IDTokenExpiresAt: t.IDTokenExpiresAt,
		IssuedAt:         t.IssuedAt,
	}, nil
}

func (a *tokenStoreAdapter) Put(ctx context.Context, t *token.OAuthToken) error {
	if t == nil {
		return nil
	}
	return a.inner.Put(ctx, serviceOAuthTokenFromInternal(t))
}

// serviceOAuthTokenFromInternal converts the internal mirrored token type into
// the service representation, preserving all delegated metadata.
func serviceOAuthTokenFromInternal(t *token.OAuthToken) *OAuthToken {
	if t == nil {
		return nil
	}
	return &OAuthToken{
		Username:         t.Username,
		Provider:         t.Provider,
		AccessToken:      t.AccessToken,
		IDToken:          t.IDToken,
		RefreshToken:     t.RefreshToken,
		ExpiresAt:        t.ExpiresAt,
		Issuer:           t.Issuer,
		Resource:         t.Resource,
		Scopes:           append([]string(nil), t.Scopes...),
		TokenType:        t.TokenType,
		Subject:          t.Subject,
		ProviderRef:      t.ProviderRef,
		ClientRef:        t.ClientRef,
		IDTokenExpiresAt: t.IDTokenExpiresAt,
		IssuedAt:         t.IssuedAt,
	}
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
	return a.inner.CASPut(ctx, serviceOAuthTokenFromInternal(t), expectedVersion, owner)
}
