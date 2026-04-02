package auth

import (
	"context"
	"time"
)

// OAuthToken represents a stored OAuth token set for a user/provider pair.
type OAuthToken struct {
	Username     string    `json:"username"`
	Provider     string    `json:"provider"`
	AccessToken  string    `json:"accessToken"`
	IDToken      string    `json:"idToken,omitempty"`
	RefreshToken string    `json:"refreshToken,omitempty"`
	ExpiresAt    time.Time `json:"expiresAt,omitempty"`
}

// TokenStore abstracts encrypted OAuth token persistence.
// Implementations may use scy-backed secrets, database storage, etc.
type TokenStore interface {
	Get(ctx context.Context, username, provider string) (*OAuthToken, error)
	Put(ctx context.Context, token *OAuthToken) error
	Delete(ctx context.Context, username, provider string) error

	// TryAcquireRefreshLease atomically attempts to acquire a distributed lease
	// for refreshing the token identified by (username, provider).
	TryAcquireRefreshLease(ctx context.Context, username, provider, owner string, ttl time.Duration) (version int64, acquired bool, err error)

	// ReleaseRefreshLease releases a previously acquired lease.
	ReleaseRefreshLease(ctx context.Context, username, provider, owner string) error

	// CASPut atomically updates the token only if the current version matches
	// expectedVersion and the lease is held by owner.
	CASPut(ctx context.Context, token *OAuthToken, expectedVersion int64, owner string) (swapped bool, err error)
}

// ExpiringTokenScanner is an optional extension of TokenStore for implementations
// that can efficiently query the store for tokens expiring before a given horizon.
// It is used by the background refresh watcher to proactively refresh tokens for
// users who are idle (no active in-memory session) before they expire.
// TokenStore implementations that do not embed a queryable DB may omit this.
type ExpiringTokenScanner interface {
	// ScanExpiring returns all stored tokens whose expiry is before horizon
	// and that carry a refresh token. Only tokens that can actually be
	// refreshed need to be returned.
	ScanExpiring(ctx context.Context, horizon time.Time) ([]*OAuthToken, error)
}
