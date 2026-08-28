package token

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	iauth "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/internal/authlog"
	"github.com/viant/agently-core/internal/logx"
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
// This mirrors service/auth.OAuthToken to avoid import cycles. The metadata
// fields are optional for legacy workspace rows; conversions and refresh/CAS
// paths must preserve them when present.
type OAuthToken struct {
	Username     string
	Provider     string
	AccessToken  string
	IDToken      string
	RefreshToken string
	ExpiresAt    time.Time

	Issuer      string
	Resource    string
	Scopes      []string
	TokenType   string
	Subject     string
	ProviderRef string
	ClientRef   string
	// IDTokenExpiresAt mirrors the verified ID-token exp; ExpiresAt remains
	// the access-token expiry for compatibility.
	IDTokenExpiresAt time.Time
	// IssuedAt records when the token set was obtained; the refresh policy
	// derives the original selected-token lifetime from it.
	IssuedAt time.Time
}

// MergeMetadataFrom copies missing metadata fields from prior. Populated
// fields on the receiver win (e.g. an authoritative refreshed scope set).
func (t *OAuthToken) MergeMetadataFrom(prior *OAuthToken) {
	if t == nil || prior == nil {
		return
	}
	if t.Issuer == "" {
		t.Issuer = prior.Issuer
	}
	if t.Resource == "" {
		t.Resource = prior.Resource
	}
	if len(t.Scopes) == 0 && len(prior.Scopes) > 0 {
		t.Scopes = append([]string(nil), prior.Scopes...)
	}
	if t.TokenType == "" {
		t.TokenType = prior.TokenType
	}
	if t.Subject == "" {
		t.Subject = prior.Subject
	}
	if t.ProviderRef == "" {
		t.ProviderRef = prior.ProviderRef
	}
	if t.ClientRef == "" {
		t.ClientRef = prior.ClientRef
	}
	if t.IssuedAt.IsZero() {
		t.IssuedAt = prior.IssuedAt
	}
	// The ID-token expiry belongs to one specific ID token value: carry it
	// over only when the receiver retained exactly the prior ID token.
	if t.IDTokenExpiresAt.IsZero() && t.IDToken != "" && t.IDToken == prior.IDToken {
		t.IDTokenExpiresAt = prior.IDTokenExpiresAt
	}
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
	mu                 sync.RWMutex
	cache              map[Key]*entry
	missCache          map[Key]time.Time
	retryCache         map[Key]time.Time
	loggedRetryCache   map[Key]time.Time
	store              TokenStore // optional persistent backing
	broker             Broker     // optional refresh/exchange (nil = cache-only)
	minTTL             time.Duration
	missTTL            time.Duration
	sf                 map[Key]*refreshInFlight
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

// WithMissTTL sets how long a missing-token result is negatively cached.
// During this window EnsureTokens skips store lookups and does not re-log the miss.
func WithMissTTL(d time.Duration) ManagerOption {
	return func(m *Manager) { m.missTTL = d }
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
		cache:            make(map[Key]*entry),
		missCache:        make(map[Key]time.Time),
		retryCache:       make(map[Key]time.Time),
		loggedRetryCache: make(map[Key]time.Time),
		sf:               make(map[Key]*refreshInFlight),
		minTTL:           2 * time.Minute,
		missTTL:          30 * time.Second,
		leaseTTL:         30 * time.Second,
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
	// Suppress per-call hot-path noise from repeated MCP/tool discovery auth checks.
	// Keep store/refresh/error logs below for troubleshooting when auth resolution
	// actually does meaningful work or fails.
	// 1. Check context — if tokens exist and not near expiry, return as-is.
	safeCtx := ctx
	resolutionFailureLogged := false
	if tok := iauth.TokensFromContext(ctx); tok != nil {
		if !tok.Expiry.IsZero() && time.Until(tok.Expiry) > m.minTTL {
			return ctx, nil
		}
		safeCtx = iauth.WithoutTokens(ctx)
	}

	// 2. Check in-memory cache — if fresh, inject into context and return.
	m.mu.RLock()
	e, ok := m.cache[key]
	missUntil, missed := m.missCache[key]
	m.mu.RUnlock()
	if ok && time.Until(e.expiresAt) > m.minTTL {
		logSchedulerEnsure(ctx, key, "oauth token cache hit expires_at=%q", e.expiresAt.UTC().Format(time.RFC3339))
		return injectTokens(ctx, e.tok), nil
	}
	if ok && e != nil && e.tok != nil {
		if retryUntil := m.loadRefreshRetryAt(key); !retryUntil.IsZero() && time.Now().Before(retryUntil) {
			if m.shouldLogRefreshRetry(key, retryUntil) {
				logSchedulerEnsure(ctx, key, "oauth token refresh cooldown active retry_at=%q", retryUntil.UTC().Format(time.RFC3339))
			}
			return safeCtx, nil
		}
	}
	if missed && time.Now().Before(missUntil) {
		return safeCtx, nil
	}

	// 3. Check persistent TokenStore (if configured).
	if m.store != nil && (!ok || time.Until(e.expiresAt) <= m.minTTL) {
		stored, err := m.store.Get(ctx, key.Subject, key.Provider)
		if err == nil && stored != nil {
			tok := oauthTokenToScy(stored)
			if time.Until(stored.ExpiresAt) > m.minTTL {
				m.cacheToken(key, tok, stored.ExpiresAt)
				m.clearMissCache(key)
				logSchedulerEnsure(ctx, key, "oauth token store hit expires_at=%q", stored.ExpiresAt.UTC().Format(time.RFC3339))
				return injectTokens(ctx, tok), nil
			}
			// Found but near-expiry — try refresh below with stored refresh token.
			e = &entry{tok: tok, expiresAt: stored.ExpiresAt}
			m.clearMissCache(key)
			logSchedulerEnsure(ctx, key, "oauth token store hit stale expires_at=%q", stored.ExpiresAt.UTC().Format(time.RFC3339))
			if retryUntil := m.loadRefreshRetryAt(key); !retryUntil.IsZero() && time.Now().Before(retryUntil) {
				if m.shouldLogRefreshRetry(key, retryUntil) {
					logSchedulerEnsure(ctx, key, "oauth token refresh cooldown active retry_at=%q", retryUntil.UTC().Format(time.RFC3339))
				}
				return safeCtx, nil
			}
		} else if err != nil {
			authlog.Log(ctx, authlog.Event{
				Op:             "scheduler_store_get",
				UserID:         strings.TrimSpace(key.Subject),
				Provider:       strings.TrimSpace(key.Provider),
				Classification: "persistence",
				Action:         "preserve_no_inject",
			})
			resolutionFailureLogged = true
		} else {
			logSchedulerEnsure(ctx, key, "oauth token store miss")
		}
	}

	// 4. If near-expiry and Broker available, refresh (mutex-serialized per key).
	if m.broker != nil && e != nil && e.tok != nil && e.tok.RefreshToken != "" {
		refreshed, err := m.serializedRefresh(ctx, key, e.tok.RefreshToken)
		if err != nil {
			if IsRefreshInvalidGrant(err) {
				action := m.clearInvalidGrantCredentials(ctx, key)
				authlog.Log(ctx, authlog.Event{
					Op:             "scheduler_refresh_action",
					UserID:         strings.TrimSpace(key.Subject),
					Provider:       strings.TrimSpace(key.Provider),
					Classification: "invalid_grant",
					Action:         action,
				})
				return safeCtx, nil
			}
			retryAt := time.Now().Add(30 * time.Second)
			m.storeRefreshRetryAt(key, retryAt)
			if m.shouldLogRefreshRetry(key, retryAt) {
				authlog.Log(ctx, authlog.Event{
					Op:             "scheduler_refresh_action",
					UserID:         strings.TrimSpace(key.Subject),
					Provider:       strings.TrimSpace(key.Provider),
					Classification: "refresh_error",
					Action:         "preserve_cooldown_no_inject",
				})
			}
			return safeCtx, nil
		}
		if refreshed != nil {
			m.clearMissCache(key)
			m.clearRefreshRetryAt(key)
			logSchedulerEnsure(ctx, key, "oauth token refresh ok")
			return injectTokens(ctx, refreshed), nil
		}
	}

	// 5. Preserve stale credentials in cache/storage, but never inject them.
	if e != nil && e.tok != nil {
		m.clearMissCache(key)
		retryAt := time.Now().Add(30 * time.Second)
		m.storeRefreshRetryAt(key, retryAt)
		if m.shouldLogRefreshRetry(key, retryAt) {
			authlog.Log(ctx, authlog.Event{
				Op:             "scheduler_token_resolve",
				UserID:         strings.TrimSpace(key.Subject),
				Provider:       strings.TrimSpace(key.Provider),
				Classification: "stale_token",
				Action:         "preserve_cooldown_no_inject",
			})
		}
		return safeCtx, nil
	}

	m.cacheMiss(key)
	if !resolutionFailureLogged {
		authlog.Log(ctx, authlog.Event{
			Op:             "scheduler_token_resolve",
			UserID:         strings.TrimSpace(key.Subject),
			Provider:       strings.TrimSpace(key.Provider),
			Classification: "token_unavailable",
			Action:         "no_inject",
		})
	}
	return safeCtx, nil
}

func (m *Manager) clearInvalidGrantCredentials(ctx context.Context, key Key) string {
	m.mu.Lock()
	delete(m.cache, key)
	delete(m.missCache, key)
	delete(m.retryCache, key)
	delete(m.loggedRetryCache, key)
	m.mu.Unlock()
	if m.store == nil {
		return "clear_cache"
	}
	if err := m.store.Delete(ctx, key.Subject, key.Provider); err != nil {
		retryAt := time.Now().Add(30 * time.Second)
		m.storeRefreshRetryAt(key, retryAt)
		return "clear_cache_delete_failed_cooldown_no_inject"
	}
	m.cacheMiss(key)
	return "clear"
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
	if m.store != nil {
		persisted := scyToOAuthToken(key, tok)
		persisted.IssuedAt = time.Now()
		if err := m.store.Put(ctx, persisted); err != nil {
			authlog.Log(ctx, authlog.Event{
				Op:             "scheduler_store_put",
				UserID:         strings.TrimSpace(key.Subject),
				Provider:       strings.TrimSpace(key.Provider),
				Classification: "persistence",
				Action:         "preserve",
			})
			return err
		}
	}
	m.cacheToken(key, tok, expiry)
	m.clearMissCache(key)
	return nil
}

// Invalidate removes cached tokens for a key.
func (m *Manager) Invalidate(ctx context.Context, key Key) error {
	m.mu.Lock()
	delete(m.cache, key)
	delete(m.missCache, key)
	m.mu.Unlock()

	if m.store != nil {
		if err := m.store.Delete(ctx, key.Subject, key.Provider); err != nil {
			authlog.Log(ctx, authlog.Event{
				Op:             "scheduler_store_delete",
				UserID:         strings.TrimSpace(key.Subject),
				Provider:       strings.TrimSpace(key.Provider),
				Classification: "persistence",
				Action:         "preserve",
			})
			return err
		}
	}
	return nil
}

// cacheToken stores a token in the in-memory cache.
func (m *Manager) cacheToken(key Key, tok *scyauth.Token, expiresAt time.Time) {
	m.mu.Lock()
	m.cache[key] = &entry{tok: tok, expiresAt: expiresAt}
	delete(m.missCache, key)
	m.mu.Unlock()
}

func (m *Manager) cacheMiss(key Key) {
	if m == nil || m.missTTL <= 0 {
		return
	}
	m.mu.Lock()
	m.missCache[key] = time.Now().Add(m.missTTL)
	m.mu.Unlock()
}

func (m *Manager) clearMissCache(key Key) {
	if m == nil {
		return
	}
	m.mu.Lock()
	delete(m.missCache, key)
	m.mu.Unlock()
}

func (m *Manager) loadRefreshRetryAt(key Key) time.Time {
	if m == nil {
		return time.Time{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if until, ok := m.retryCache[key]; ok {
		if time.Now().Before(until) {
			return until
		}
		delete(m.retryCache, key)
		delete(m.loggedRetryCache, key)
	}
	return time.Time{}
}

func (m *Manager) storeRefreshRetryAt(key Key, until time.Time) {
	if m == nil || until.IsZero() {
		return
	}
	m.mu.Lock()
	m.retryCache[key] = until
	m.mu.Unlock()
}

func (m *Manager) clearRefreshRetryAt(key Key) {
	if m == nil {
		return
	}
	m.mu.Lock()
	delete(m.retryCache, key)
	delete(m.loggedRetryCache, key)
	m.mu.Unlock()
}

func (m *Manager) shouldLogRefreshRetry(key Key, until time.Time) bool {
	if m == nil || until.IsZero() {
		return true
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if prev, ok := m.loggedRetryCache[key]; ok && prev.Equal(until) {
		return false
	}
	m.loggedRetryCache[key] = until
	return true
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

	logSchedulerEnsure(ctx, key, "oauth token refresh start")
	tok, err := m.broker.Refresh(ctx, key, refreshToken)
	if err != nil {
		return nil, err
	}
	if tok == nil {
		return nil, nil
	}

	expiry := tok.Expiry
	if expiry.IsZero() {
		expiry = time.Now().Add(1 * time.Hour)
	}

	// Update persistent store.
	if m.store != nil {
		if err := m.store.Put(ctx, m.persistableRefreshToken(ctx, key, tok)); err != nil {
			authlog.Log(ctx, authlog.Event{
				Op:             "scheduler_store_put_candidate",
				UserID:         strings.TrimSpace(key.Subject),
				Provider:       strings.TrimSpace(key.Provider),
				Classification: "persistence",
				Action:         "preserve_cooldown_no_inject",
			})
			return nil, err
		}
	}
	m.cacheToken(key, tok, expiry)

	return tok, nil
}

// persistableRefreshToken builds the persistable row for a refresh result.
// scyauth.Token cannot carry delegated credential metadata, so the previously
// stored row's issuer/resource/scopes/tokenType/subject are re-attached — a
// refresh must never drop stored delegated metadata through Put or CASPut.
//
// The merge requires an exact provider match: legacy Get implementations may
// fall back to a different provider row, and metadata from another provider —
// in particular a delegated (mcp:v1) row — must never be copied into a
// workspace token.
func (m *Manager) persistableRefreshToken(ctx context.Context, key Key, tok *scyauth.Token) *OAuthToken {
	next := scyToOAuthToken(key, tok)
	next.IssuedAt = time.Now()
	if m != nil && m.store != nil {
		if prior, err := m.store.Get(ctx, key.Subject, key.Provider); err == nil && prior != nil &&
			strings.TrimSpace(prior.Provider) == strings.TrimSpace(key.Provider) {
			next.MergeMetadataFrom(prior)
		}
	}
	return next
}

// distributedRefresh coordinates token refresh across multiple pods using a DB-level lease.
// It uses TryAcquireRefreshLease as L2 (distributed lock) after the L1 in-process lock.
func (m *Manager) distributedRefresh(ctx context.Context, key Key, refreshToken string) (*scyauth.Token, error) {
	owner := string(m.instanceID)

	// Try to acquire the distributed lease.
	version, acquired, err := m.store.TryAcquireRefreshLease(ctx, key.Subject, key.Provider, owner, m.leaseTTL)
	if err != nil {
		authlog.Log(ctx, authlog.Event{
			Op:             "scheduler_lease_acquire",
			UserID:         strings.TrimSpace(key.Subject),
			Provider:       strings.TrimSpace(key.Provider),
			Classification: "lease",
			Action:         "preserve_cooldown_no_inject",
		})
		return nil, err
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
		if err != nil {
			authlog.Log(ctx, authlog.Event{
				Op:             "scheduler_store_get_after_lease",
				UserID:         strings.TrimSpace(key.Subject),
				Provider:       strings.TrimSpace(key.Provider),
				Classification: "persistence",
				Action:         "preserve_no_inject",
			})
		}
		// Still stale — return nil so the caller preserves without injection.
		return nil, nil
	}

	// Lease acquired — perform the actual refresh.
	logSchedulerEnsure(ctx, key, "oauth token refresh start")
	tok, err := m.broker.Refresh(ctx, key, refreshToken)
	if err != nil {
		// Release the lease so another pod can try.
		if releaseErr := m.store.ReleaseRefreshLease(ctx, key.Subject, key.Provider, owner); releaseErr != nil {
			authlog.Log(ctx, authlog.Event{
				Op:             "scheduler_lease_release",
				UserID:         strings.TrimSpace(key.Subject),
				Provider:       strings.TrimSpace(key.Provider),
				Classification: "lease",
				Action:         "preserve",
			})
		}
		return nil, err
	}
	if tok == nil {
		if releaseErr := m.store.ReleaseRefreshLease(ctx, key.Subject, key.Provider, owner); releaseErr != nil {
			authlog.Log(ctx, authlog.Event{
				Op:             "scheduler_lease_release",
				UserID:         strings.TrimSpace(key.Subject),
				Provider:       strings.TrimSpace(key.Provider),
				Classification: "lease",
				Action:         "preserve",
			})
		}
		return nil, nil
	}

	expiry := tok.Expiry
	if expiry.IsZero() {
		expiry = time.Now().Add(1 * time.Hour)
	}

	// CAS-write the new token — only succeeds if version hasn't changed.
	oauthTok := m.persistableRefreshToken(ctx, key, tok)
	swapped, err := m.store.CASPut(ctx, oauthTok, version, owner)
	if err != nil {
		if releaseErr := m.store.ReleaseRefreshLease(ctx, key.Subject, key.Provider, owner); releaseErr != nil {
			authlog.Log(ctx, authlog.Event{
				Op:             "scheduler_lease_release",
				UserID:         strings.TrimSpace(key.Subject),
				Provider:       strings.TrimSpace(key.Provider),
				Classification: "lease",
				Action:         "preserve",
			})
		}
		authlog.Log(ctx, authlog.Event{
			Op:             "scheduler_cas_put",
			UserID:         strings.TrimSpace(key.Subject),
			Provider:       strings.TrimSpace(key.Provider),
			Classification: "persistence",
			Action:         "preserve_cooldown_no_inject",
		})
		return nil, err
	}
	if !swapped {
		// Another pod won the race — discard our result, re-read store.
		stored, err := m.store.Get(ctx, key.Subject, key.Provider)
		if err != nil {
			authlog.Log(ctx, authlog.Event{
				Op:             "scheduler_store_get_after_cas",
				UserID:         strings.TrimSpace(key.Subject),
				Provider:       strings.TrimSpace(key.Provider),
				Classification: "persistence",
				Action:         "preserve_no_inject",
			})
			return nil, err
		}
		if stored != nil && time.Until(stored.ExpiresAt) > m.minTTL {
			tok = oauthTokenToScy(stored)
			expiry = stored.ExpiresAt
		} else {
			return nil, nil
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
	if tok == nil {
		return nil, nil
	}
	expiry := tok.Expiry
	if expiry.IsZero() {
		expiry = time.Now().Add(1 * time.Hour)
	}
	if m.store != nil {
		if err := m.store.Put(ctx, m.persistableRefreshToken(ctx, key, tok)); err != nil {
			authlog.Log(ctx, authlog.Event{
				Op:             "scheduler_store_put_candidate",
				UserID:         strings.TrimSpace(key.Subject),
				Provider:       strings.TrimSpace(key.Provider),
				Classification: "persistence",
				Action:         "preserve_cooldown_no_inject",
			})
			return nil, err
		}
	}
	m.cacheToken(key, tok, expiry)
	return tok, nil
}

// injectTokens enriches a context with token data via iauth helpers.
func injectTokens(ctx context.Context, tok *scyauth.Token) context.Context {
	if tok == nil || (!tok.Expiry.IsZero() && !tok.Expiry.After(time.Now())) {
		return iauth.WithoutTokens(ctx)
	}
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

func logSchedulerEnsure(ctx context.Context, key Key, format string, args ...interface{}) {
	message := authlog.Sanitize(fmt.Sprintf(format, args...))
	logx.DebugCtxf(ctx, "auth-token", "user_id=%q provider=%q %s",
		authlog.SafeUserID(key.Subject), authlog.Sanitize(key.Provider), message)
}
