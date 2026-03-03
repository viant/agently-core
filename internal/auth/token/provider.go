package token

import (
	"context"
	"strings"
	"sync"
	"time"

	iauth "github.com/viant/agently-core/internal/auth"
	scyauth "github.com/viant/scy/auth"
	"golang.org/x/oauth2"
)

// Provider supplies fresh tokens for a user+provider pair.
type Provider interface {
	// EnsureTokens checks if tokens in context are fresh; if not, refreshes
	// from cache or via Broker, and returns updated context.
	EnsureTokens(ctx context.Context, key Key) (context.Context, error)

	// Store persists tokens for later retrieval (called by auth middleware on login/callback).
	Store(ctx context.Context, key Key, tok *scyauth.Token) error

	// Invalidate removes cached tokens for a key (called on logout).
	Invalidate(ctx context.Context, key Key) error
}

// OAuthToken represents a stored OAuth token set for a user/provider pair.
// This mirrors service/auth.OAuthToken to avoid import cycles.
type OAuthToken struct {
	Username     string
	Provider     string
	AccessToken  string
	IDToken      string
	RefreshToken string
	ExpiresAt    time.Time
}

// TokenStore abstracts encrypted OAuth token persistence.
// This mirrors service/auth.TokenStore to avoid import cycles.
// Implementations from service/auth satisfy this interface.
type TokenStore interface {
	Get(ctx context.Context, username, provider string) (*OAuthToken, error)
	Put(ctx context.Context, token *OAuthToken) error
	Delete(ctx context.Context, username, provider string) error

	// TryAcquireRefreshLease atomically attempts to acquire a distributed lease
	// for refreshing the token identified by (username, provider). Returns the
	// current version and whether the lease was acquired.
	TryAcquireRefreshLease(ctx context.Context, username, provider, owner string, ttl time.Duration) (version int64, acquired bool, err error)

	// ReleaseRefreshLease releases a previously acquired lease (e.g. on failure).
	ReleaseRefreshLease(ctx context.Context, username, provider, owner string) error

	// CASPut atomically updates the token only if the current version matches
	// expectedVersion and the lease is held by owner. Returns whether the swap succeeded.
	CASPut(ctx context.Context, token *OAuthToken, expectedVersion int64, owner string) (swapped bool, err error)
}

// entry holds a cached token with its expiry.
type entry struct {
	tok       *scyauth.Token
	expiresAt time.Time
}

// refreshInFlight is a per-key mutex to prevent concurrent refreshes.
type refreshInFlight struct {
	mu sync.Mutex
}

// Manager is the default in-process Provider implementation.
// It layers an in-memory cache over an optional persistent TokenStore
// and uses an optional Broker for refresh/exchange.
type Manager struct {
	mu         sync.RWMutex
	cache      map[Key]*entry
	store      TokenStore // optional persistent backing
	broker     Broker     // optional refresh/exchange (nil = cache-only)
	minTTL     time.Duration
	sf         map[Key]*refreshInFlight
	instanceID         InstanceID    // when set, enables distributed refresh coordination
	instanceIDExplicit bool          // true when WithInstanceID was called (even with "")
	leaseTTL           time.Duration // distributed lease duration (default 30s)
}

// ManagerOption configures a Manager.
type ManagerOption func(*Manager)

// WithTokenStore sets the persistent token store.
func WithTokenStore(s TokenStore) ManagerOption {
	return func(m *Manager) { m.store = s }
}

// WithBroker sets the token broker for refresh/exchange.
func WithBroker(b Broker) ManagerOption {
	return func(m *Manager) { m.broker = b }
}

// WithMinTTL sets the minimum remaining TTL before a refresh is triggered.
func WithMinTTL(d time.Duration) ManagerOption {
	return func(m *Manager) { m.minTTL = d }
}

// WithInstanceID sets the instance identity for distributed refresh coordination.
// Pass a non-empty InstanceID to enable, or "" to explicitly disable auto-detection.
func WithInstanceID(id InstanceID) ManagerOption {
	return func(m *Manager) {
		m.instanceID = id
		m.instanceIDExplicit = true
	}
}

// WithLeaseTTL sets the distributed refresh lease duration (default 30s).
func WithLeaseTTL(d time.Duration) ManagerOption {
	return func(m *Manager) { m.leaseTTL = d }
}

// NewManager creates a new token Manager.
// When a TokenStore is provided and no explicit InstanceID is set, distributed
// refresh coordination is automatically enabled with an auto-generated InstanceID.
// To explicitly disable distributed mode, use WithInstanceID("").
func NewManager(opts ...ManagerOption) *Manager {
	m := &Manager{
		cache:    make(map[Key]*entry),
		sf:       make(map[Key]*refreshInFlight),
		minTTL:   2 * time.Minute,
		leaseTTL: 30 * time.Second,
	}
	for _, o := range opts {
		o(m)
	}
	// Auto-enable distributed refresh when a store is present and no explicit
	// InstanceID was set (instanceID == "" and store != nil).
	if m.store != nil && m.instanceID == "" && !m.instanceIDExplicit {
		m.instanceID = NewInstanceID()
	}
	return m
}

// EnsureTokens checks if tokens in context are fresh; if not, refreshes from
// cache or via Broker, and returns updated context.
func (m *Manager) EnsureTokens(ctx context.Context, key Key) (context.Context, error) {
	// 1. Check context — if tokens exist and not near expiry, return as-is.
	if tok := iauth.TokensFromContext(ctx); tok != nil {
		if !tok.Expiry.IsZero() && time.Until(tok.Expiry) > m.minTTL {
			return ctx, nil
		}
	}

	// 2. Check in-memory cache — if fresh, inject into context and return.
	m.mu.RLock()
	e, ok := m.cache[key]
	m.mu.RUnlock()
	if ok && time.Until(e.expiresAt) > m.minTTL {
		return injectTokens(ctx, e.tok), nil
	}

	// 3. Check persistent TokenStore (if configured).
	if m.store != nil && (!ok || time.Until(e.expiresAt) <= m.minTTL) {
		stored, err := m.store.Get(ctx, key.Subject, key.Provider)
		if err == nil && stored != nil {
			tok := oauthTokenToScy(stored)
			if time.Until(stored.ExpiresAt) > m.minTTL {
				m.cacheToken(key, tok, stored.ExpiresAt)
				return injectTokens(ctx, tok), nil
			}
			// Found but near-expiry — try refresh below with stored refresh token.
			e = &entry{tok: tok, expiresAt: stored.ExpiresAt}
		}
	}

	// 4. If near-expiry and Broker available, refresh (mutex-serialized per key).
	if m.broker != nil && e != nil && e.tok != nil && e.tok.RefreshToken != "" {
		refreshed, err := m.serializedRefresh(ctx, key, e.tok.RefreshToken)
		if err != nil {
			return ctx, err
		}
		if refreshed != nil {
			return injectTokens(ctx, refreshed), nil
		}
	}

	// 5. If we have a cached (possibly stale) token, still inject it.
	if e != nil && e.tok != nil {
		return injectTokens(ctx, e.tok), nil
	}

	return ctx, nil
}

// Store persists tokens for later retrieval.
func (m *Manager) Store(ctx context.Context, key Key, tok *scyauth.Token) error {
	if tok == nil {
		return nil
	}
	expiry := tok.Expiry
	if expiry.IsZero() {
		expiry = time.Now().Add(1 * time.Hour)
	}
	m.cacheToken(key, tok, expiry)

	if m.store != nil {
		return m.store.Put(ctx, scyToOAuthToken(key, tok))
	}
	return nil
}

// Invalidate removes cached tokens for a key.
func (m *Manager) Invalidate(ctx context.Context, key Key) error {
	m.mu.Lock()
	delete(m.cache, key)
	m.mu.Unlock()

	if m.store != nil {
		return m.store.Delete(ctx, key.Subject, key.Provider)
	}
	return nil
}

// cacheToken stores a token in the in-memory cache.
func (m *Manager) cacheToken(key Key, tok *scyauth.Token, expiresAt time.Time) {
	m.mu.Lock()
	m.cache[key] = &entry{tok: tok, expiresAt: expiresAt}
	m.mu.Unlock()
}

// serializedRefresh performs a broker refresh with per-key mutex serialization (L1 in-process lock).
// When instanceID is set and a store is available, it delegates to distributedRefresh for cross-pod coordination.
func (m *Manager) serializedRefresh(ctx context.Context, key Key, refreshToken string) (*scyauth.Token, error) {
	m.mu.Lock()
	inf, ok := m.sf[key]
	if !ok {
		inf = &refreshInFlight{}
		m.sf[key] = inf
	}
	m.mu.Unlock()

	inf.mu.Lock()
	defer inf.mu.Unlock()

	// Double-check cache after acquiring lock — another goroutine may have refreshed.
	m.mu.RLock()
	e, ok := m.cache[key]
	m.mu.RUnlock()
	if ok && time.Until(e.expiresAt) > m.minTTL {
		return e.tok, nil
	}

	// If distributed mode is enabled, use DB-level lease coordination.
	if m.instanceID != "" && m.store != nil {
		return m.distributedRefresh(ctx, key, refreshToken)
	}

	tok, err := m.broker.Refresh(ctx, key, refreshToken)
	if err != nil {
		return nil, err
	}

	expiry := tok.Expiry
	if expiry.IsZero() {
		expiry = time.Now().Add(1 * time.Hour)
	}
	m.cacheToken(key, tok, expiry)

	// Update persistent store.
	if m.store != nil {
		_ = m.store.Put(ctx, scyToOAuthToken(key, tok))
	}

	return tok, nil
}

// distributedRefresh coordinates token refresh across multiple pods using a DB-level lease.
// It uses TryAcquireRefreshLease as L2 (distributed lock) after the L1 in-process lock.
func (m *Manager) distributedRefresh(ctx context.Context, key Key, refreshToken string) (*scyauth.Token, error) {
	owner := string(m.instanceID)

	// Try to acquire the distributed lease.
	version, acquired, err := m.store.TryAcquireRefreshLease(ctx, key.Subject, key.Provider, owner, m.leaseTTL)
	if err != nil {
		// DB error — fall back to local-only refresh.
		return m.localRefresh(ctx, key, refreshToken)
	}

	if !acquired {
		// Another pod is refreshing. Wait briefly, then re-read from store.
		time.Sleep(500 * time.Millisecond)
		stored, err := m.store.Get(ctx, key.Subject, key.Provider)
		if err == nil && stored != nil && time.Until(stored.ExpiresAt) > m.minTTL {
			tok := oauthTokenToScy(stored)
			m.cacheToken(key, tok, stored.ExpiresAt)
			return tok, nil
		}
		// Still stale — return nil so caller uses cached token.
		return nil, nil
	}

	// Lease acquired — perform the actual refresh.
	tok, err := m.broker.Refresh(ctx, key, refreshToken)
	if err != nil {
		// Release the lease so another pod can try.
		_ = m.store.ReleaseRefreshLease(ctx, key.Subject, key.Provider, owner)
		return nil, err
	}

	expiry := tok.Expiry
	if expiry.IsZero() {
		expiry = time.Now().Add(1 * time.Hour)
	}

	// CAS-write the new token — only succeeds if version hasn't changed.
	oauthTok := scyToOAuthToken(key, tok)
	swapped, err := m.store.CASPut(ctx, oauthTok, version, owner)
	if err != nil {
		_ = m.store.ReleaseRefreshLease(ctx, key.Subject, key.Provider, owner)
		return nil, err
	}
	if !swapped {
		// Another pod won the race — discard our result, re-read store.
		stored, err := m.store.Get(ctx, key.Subject, key.Provider)
		if err == nil && stored != nil {
			tok = oauthTokenToScy(stored)
			expiry = stored.ExpiresAt
		}
	}

	m.cacheToken(key, tok, expiry)
	return tok, nil
}

// localRefresh performs a broker refresh without distributed coordination (fallback).
func (m *Manager) localRefresh(ctx context.Context, key Key, refreshToken string) (*scyauth.Token, error) {
	tok, err := m.broker.Refresh(ctx, key, refreshToken)
	if err != nil {
		return nil, err
	}
	expiry := tok.Expiry
	if expiry.IsZero() {
		expiry = time.Now().Add(1 * time.Hour)
	}
	m.cacheToken(key, tok, expiry)
	if m.store != nil {
		_ = m.store.Put(ctx, scyToOAuthToken(key, tok))
	}
	return tok, nil
}

// injectTokens enriches a context with token data via iauth helpers.
func injectTokens(ctx context.Context, tok *scyauth.Token) context.Context {
	ctx = iauth.WithTokens(ctx, tok)
	if strings.TrimSpace(tok.AccessToken) != "" {
		ctx = iauth.WithBearer(ctx, tok.AccessToken)
	}
	if strings.TrimSpace(tok.IDToken) != "" {
		ctx = iauth.WithIDToken(ctx, tok.IDToken)
	}
	return ctx
}

// oauthTokenToScy converts an OAuthToken to scy auth Token.
func oauthTokenToScy(t *OAuthToken) *scyauth.Token {
	return &scyauth.Token{
		Token: oauth2.Token{
			AccessToken:  t.AccessToken,
			RefreshToken: t.RefreshToken,
			Expiry:       t.ExpiresAt,
		},
		IDToken: t.IDToken,
	}
}

// scyToOAuthToken converts a scy auth Token to OAuthToken.
func scyToOAuthToken(key Key, tok *scyauth.Token) *OAuthToken {
	return &OAuthToken{
		Username:     key.Subject,
		Provider:     key.Provider,
		AccessToken:  tok.AccessToken,
		IDToken:      tok.IDToken,
		RefreshToken: tok.RefreshToken,
		ExpiresAt:    tok.Expiry,
	}
}
