package auth

import (
	"context"
	"sync"
	"testing"
	"time"
)

// fakeScanStore implements TokenStore + ExpiringTokenScanner with canned rows.
type fakeScanStore struct {
	rows    []*OAuthToken
	mu      sync.Mutex
	puts    int
	deletes int
}

func (f *fakeScanStore) Get(ctx context.Context, username, provider string) (*OAuthToken, error) {
	return nil, nil
}
func (f *fakeScanStore) Put(ctx context.Context, token *OAuthToken) error {
	f.mu.Lock()
	f.puts++
	f.mu.Unlock()
	return nil
}
func (f *fakeScanStore) Delete(ctx context.Context, username, provider string) error {
	f.mu.Lock()
	f.deletes++
	f.mu.Unlock()
	return nil
}
func (f *fakeScanStore) TryAcquireRefreshLease(ctx context.Context, username, provider, owner string, ttl time.Duration) (int64, bool, error) {
	return 0, false, nil
}
func (f *fakeScanStore) ReleaseRefreshLease(ctx context.Context, username, provider, owner string) error {
	return nil
}
func (f *fakeScanStore) CASPut(ctx context.Context, token *OAuthToken, expectedVersion int64, owner string) (bool, error) {
	return false, nil
}
func (f *fakeScanStore) ScanExpiring(ctx context.Context, horizon time.Time) ([]*OAuthToken, error) {
	return f.rows, nil
}

type recordingDelegatedRefresher struct {
	mu      sync.Mutex
	rows    []*OAuthToken
	maxLead time.Duration
}

func (r *recordingDelegatedRefresher) RefreshStoredDelegatedToken(ctx context.Context, stored *OAuthToken) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rows = append(r.rows, stored)
	return nil
}

func (r *recordingDelegatedRefresher) MaxRefreshLead(ctx context.Context) time.Duration {
	return r.maxLead
}

func delegatedWatcherRuntime(store *fakeScanStore) *Runtime {
	cfg := &Config{Enabled: true, OAuth: &OAuth{Name: "corp-idp"}}
	return &Runtime{
		cfg: cfg,
		ext: &authExtension{cfg: cfg, tokenStore: store},
	}
}

func TestRefreshTokenStoreRoutesDelegatedRows(t *testing.T) {
	delegatedKey := DelegatedProviderStorageKey("ns", "adelphic-dev6")
	store := &fakeScanStore{rows: []*OAuthToken{
		{Username: "uuid-1", Provider: delegatedKey, ProviderRef: "adelphic-dev6", RefreshToken: "rt", ExpiresAt: time.Now().Add(time.Minute)},
		{Username: "uuid-1", Provider: "github", RefreshToken: "rt", ExpiresAt: time.Now().Add(time.Minute)},
	}}
	runtime := delegatedWatcherRuntime(store)
	refresher := &recordingDelegatedRefresher{maxLead: time.Hour}
	runtime.delegatedRefresher = refresher

	runtime.refreshTokenStore(context.Background())

	refresher.mu.Lock()
	defer refresher.mu.Unlock()
	if len(refresher.rows) != 1 || refresher.rows[0].Provider != delegatedKey {
		t.Fatalf("delegated row must route to the delegated refresher, got %d rows", len(refresher.rows))
	}
	// The unknown "github" row is skipped: never sent to the workspace path,
	// never mutated.
	if store.puts != 0 || store.deletes != 0 {
		t.Fatalf("unknown provider rows must never be mutated (puts=%d deletes=%d)", store.puts, store.deletes)
	}
}

func TestRefreshTokenStoreSkipsDelegatedRowsWithoutRefresher(t *testing.T) {
	delegatedKey := DelegatedProviderStorageKey("ns", "adelphic-dev6")
	store := &fakeScanStore{rows: []*OAuthToken{
		{Username: "uuid-1", Provider: delegatedKey, ProviderRef: "adelphic-dev6", RefreshToken: "rt", ExpiresAt: time.Now().Add(time.Minute)},
	}}
	runtime := delegatedWatcherRuntime(store)
	// No delegated refresher installed: rows are skipped without mutation and
	// never reach the workspace refresh path (which would panic on the nil
	// OAuth client config in this fixture if invoked).
	runtime.refreshTokenStore(context.Background())
	if store.puts != 0 || store.deletes != 0 {
		t.Fatalf("delegated rows without routing must never be mutated")
	}
}

func TestStoreScanHorizonUsesLargestLead(t *testing.T) {
	runtime := delegatedWatcherRuntime(&fakeScanStore{})
	// Default: shared 15-minute lead.
	if got := runtime.storeScanHorizon(context.Background()); got != 15*time.Minute {
		t.Fatalf("default horizon = %s, want 15m", got)
	}
	// A provider with a larger lead extends the broad horizon.
	runtime.delegatedRefresher = &recordingDelegatedRefresher{maxLead: time.Hour}
	if got := runtime.storeScanHorizon(context.Background()); got != time.Hour {
		t.Fatalf("horizon = %s, want 1h", got)
	}
	// The workspace lead still wins when larger.
	runtime.cfg.TokenRefreshLeadMinutes = 90
	if got := runtime.storeScanHorizon(context.Background()); got != 90*time.Minute {
		t.Fatalf("horizon = %s, want 90m", got)
	}
}

// TestMergeableRefreshPrior covers the workspace refresh metadata-merge guard:
// only the exact same provider row may donate metadata, and delegated rows are
// refused outright.
func TestMergeableRefreshPrior(t *testing.T) {
	delegatedKey := DelegatedProviderStorageKey("ns", "adelphic-dev6")
	if mergeableRefreshPrior(nil, "corp-idp") {
		t.Fatalf("nil prior must not merge")
	}
	if mergeableRefreshPrior(&OAuthToken{Provider: "github"}, "corp-idp") {
		t.Fatalf("a fallback row from another provider must not merge")
	}
	if mergeableRefreshPrior(&OAuthToken{Provider: delegatedKey, ProviderRef: "adelphic-dev6"}, "corp-idp") {
		t.Fatalf("delegated metadata must never merge into a workspace token")
	}
	// Even an exact delegated match is refused on the workspace merge path.
	if mergeableRefreshPrior(&OAuthToken{Provider: delegatedKey}, delegatedKey) {
		t.Fatalf("the workspace refresh path must never adopt delegated rows")
	}
	if !mergeableRefreshPrior(&OAuthToken{Provider: "corp-idp"}, "corp-idp") {
		t.Fatalf("the exact workspace row must merge")
	}
}

// TestStoredTokenLifetime covers issued-at derivation for the refresh-policy
// clamp: persisted metadata first, then the access-token iat claim, else zero.
func TestStoredTokenLifetime(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	withMetadata := &OAuthToken{IssuedAt: now.Add(-30 * time.Minute), ExpiresAt: now.Add(30 * time.Minute)}
	if got := storedTokenLifetime(withMetadata); got != time.Hour {
		t.Fatalf("metadata lifetime = %s, want 1h", got)
	}
	jwtAccess := &OAuthToken{
		AccessToken: unsafeJWT(t, map[string]interface{}{
			"iat": float64(now.Add(-10 * time.Minute).Unix()),
			"exp": float64(now.Add(50 * time.Minute).Unix()),
		}),
		ExpiresAt: now.Add(50 * time.Minute),
	}
	if got := storedTokenLifetime(jwtAccess); got != time.Hour {
		t.Fatalf("iat-derived lifetime = %s, want 1h", got)
	}
	if got := storedTokenLifetime(&OAuthToken{AccessToken: "opaque", ExpiresAt: now.Add(time.Hour)}); got != 0 {
		t.Fatalf("unknown issued-at must yield zero lifetime, got %s", got)
	}
	if got := storedTokenLifetime(&OAuthToken{IssuedAt: now.Add(time.Minute), ExpiresAt: now}); got != 0 {
		t.Fatalf("inverted issued-at/expiry must yield zero lifetime, got %s", got)
	}
}

// TestDelegatedTokenEncryptionSalt covers explicit-key selection, the legacy
// configURL fallback and the empty (fail-loud) case.
func TestDelegatedTokenEncryptionSalt(t *testing.T) {
	var nilCfg *Config
	if got := nilCfg.DelegatedTokenEncryptionSalt(); got != "" {
		t.Fatalf("nil config salt = %q", got)
	}
	explicit := &Config{TokenEncryptionKey: " explicit-key ", OAuth: &OAuth{Client: &OAuthClient{ConfigURL: "scy://legacy"}}}
	if got := explicit.DelegatedTokenEncryptionSalt(); got != "explicit-key" {
		t.Fatalf("explicit key must win, got %q", got)
	}
	fallback := &Config{OAuth: &OAuth{Client: &OAuthClient{ConfigURL: "scy://legacy"}}}
	if got := fallback.DelegatedTokenEncryptionSalt(); got != "scy://legacy" {
		t.Fatalf("configURL fallback = %q", got)
	}
	// JWT/local-only workspaces: the explicit key works without any OAuth client.
	jwtOnly := &Config{JWT: &JWT{Enabled: true}, TokenEncryptionKey: "jwt-workspace-key"}
	if got := jwtOnly.DelegatedTokenEncryptionSalt(); got != "jwt-workspace-key" {
		t.Fatalf("jwt-only workspaces must use the explicit key, got %q", got)
	}
	if got := (&Config{JWT: &JWT{Enabled: true}}).DelegatedTokenEncryptionSalt(); got != "" {
		t.Fatalf("no key and no oauth client must yield empty salt, got %q", got)
	}
}

// TestTokenEncryptionKeyEnvExpansion proves the key is env-expandable through
// the shared auth config template expansion.
func TestTokenEncryptionKeyEnvExpansion(t *testing.T) {
	t.Setenv("AGENTLY_TEST_TOKEN_KEY", "expanded-secret")
	cfg := &Config{TokenEncryptionKey: "${AGENTLY_TEST_TOKEN_KEY}"}
	expandAuthEnvTemplates(cfg)
	if cfg.TokenEncryptionKey != "expanded-secret" {
		t.Fatalf("tokenEncryptionKey must expand env templates, got %q", cfg.TokenEncryptionKey)
	}
	cfg = &Config{TokenEncryptionKey: "${AGENTLY_TEST_TOKEN_KEY_MISSING:-default-key}"}
	expandAuthEnvTemplates(cfg)
	if cfg.TokenEncryptionKey != "default-key" {
		t.Fatalf("tokenEncryptionKey must honor template defaults, got %q", cfg.TokenEncryptionKey)
	}
}

func TestTokenRefreshLeadDefaultIsFifteenMinutes(t *testing.T) {
	cfg := &Config{}
	if got := cfg.tokenRefreshLead(); got != 15*time.Minute {
		t.Fatalf("default workspace refresh lead = %s, want 15m", got)
	}
	cfg.TokenRefreshLeadMinutes = 30
	if got := cfg.tokenRefreshLead(); got != 30*time.Minute {
		t.Fatalf("configured lead = %s, want 30m", got)
	}
}
