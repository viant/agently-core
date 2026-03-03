package token

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	scyauth "github.com/viant/scy/auth"
	"golang.org/x/oauth2"
)

// --- mock TokenStore ---

type mockTokenStore struct {
	mu sync.Mutex

	getFunc     func(ctx context.Context, username, provider string) (*OAuthToken, error)
	putFunc     func(ctx context.Context, token *OAuthToken) error
	deleteFunc  func(ctx context.Context, username, provider string) error
	acquireFunc func(ctx context.Context, username, provider, owner string, ttl time.Duration) (int64, bool, error)
	releaseFunc func(ctx context.Context, username, provider, owner string) error
	casFunc     func(ctx context.Context, token *OAuthToken, expectedVersion int64, owner string) (bool, error)

	acquireCalls int
	releaseCalls int
	casCalls     int
	putCalls     int
}

func (m *mockTokenStore) Get(ctx context.Context, username, provider string) (*OAuthToken, error) {
	if m.getFunc != nil {
		return m.getFunc(ctx, username, provider)
	}
	return nil, nil
}
func (m *mockTokenStore) Put(ctx context.Context, token *OAuthToken) error {
	m.mu.Lock()
	m.putCalls++
	m.mu.Unlock()
	if m.putFunc != nil {
		return m.putFunc(ctx, token)
	}
	return nil
}
func (m *mockTokenStore) Delete(ctx context.Context, username, provider string) error {
	if m.deleteFunc != nil {
		return m.deleteFunc(ctx, username, provider)
	}
	return nil
}
func (m *mockTokenStore) TryAcquireRefreshLease(ctx context.Context, username, provider, owner string, ttl time.Duration) (int64, bool, error) {
	m.mu.Lock()
	m.acquireCalls++
	m.mu.Unlock()
	if m.acquireFunc != nil {
		return m.acquireFunc(ctx, username, provider, owner, ttl)
	}
	return 0, false, nil
}
func (m *mockTokenStore) ReleaseRefreshLease(ctx context.Context, username, provider, owner string) error {
	m.mu.Lock()
	m.releaseCalls++
	m.mu.Unlock()
	if m.releaseFunc != nil {
		return m.releaseFunc(ctx, username, provider, owner)
	}
	return nil
}
func (m *mockTokenStore) CASPut(ctx context.Context, token *OAuthToken, expectedVersion int64, owner string) (bool, error) {
	m.mu.Lock()
	m.casCalls++
	m.mu.Unlock()
	if m.casFunc != nil {
		return m.casFunc(ctx, token, expectedVersion, owner)
	}
	return true, nil
}

// --- mock Broker ---

type mockBroker struct {
	refreshFunc  func(ctx context.Context, key Key, refreshToken string) (*scyauth.Token, error)
	exchangeFunc func(ctx context.Context, key Key, code string) (*scyauth.Token, error)

	mu           sync.Mutex
	refreshCalls int
}

func (b *mockBroker) Refresh(ctx context.Context, key Key, refreshToken string) (*scyauth.Token, error) {
	b.mu.Lock()
	b.refreshCalls++
	b.mu.Unlock()
	if b.refreshFunc != nil {
		return b.refreshFunc(ctx, key, refreshToken)
	}
	return nil, errors.New("broker: not configured")
}
func (b *mockBroker) Exchange(ctx context.Context, key Key, code string) (*scyauth.Token, error) {
	if b.exchangeFunc != nil {
		return b.exchangeFunc(ctx, key, code)
	}
	return nil, errors.New("broker: not configured")
}

// --- helpers ---

func freshToken(access, refresh string, expiry time.Time) *scyauth.Token {
	return &scyauth.Token{
		Token: oauth2.Token{
			AccessToken:  access,
			RefreshToken: refresh,
			Expiry:       expiry,
		},
	}
}

var testKey = Key{Subject: "user1", Provider: "google"}

// TestDistributedRefresh_LeaseAcquired verifies the happy path:
// lease acquired → broker refresh → CASPut succeeds → token cached.
func TestDistributedRefresh_LeaseAcquired(t *testing.T) {
	store := &mockTokenStore{
		getFunc: func(_ context.Context, _, _ string) (*OAuthToken, error) {
			return &OAuthToken{
				Username:     "user1",
				Provider:     "google",
				AccessToken:  "old-access",
				RefreshToken: "old-refresh",
				ExpiresAt:    time.Now().Add(-1 * time.Minute), // expired
			}, nil
		},
		acquireFunc: func(_ context.Context, _, _, _ string, _ time.Duration) (int64, bool, error) {
			return 1, true, nil
		},
		casFunc: func(_ context.Context, _ *OAuthToken, ver int64, _ string) (bool, error) {
			if ver != 1 {
				t.Errorf("expected version 1, got %d", ver)
			}
			return true, nil
		},
	}

	newExpiry := time.Now().Add(1 * time.Hour)
	broker := &mockBroker{
		refreshFunc: func(_ context.Context, _ Key, _ string) (*scyauth.Token, error) {
			return freshToken("new-access", "new-refresh", newExpiry), nil
		},
	}

	mgr := NewManager(
		WithTokenStore(store),
		WithBroker(broker),
		WithInstanceID("host:1:uuid-1"),
		WithLeaseTTL(30*time.Second),
	)

	tok, err := mgr.serializedRefresh(context.Background(), testKey, "old-refresh")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tok == nil {
		t.Fatal("expected non-nil token")
	}
	if tok.AccessToken != "new-access" {
		t.Errorf("expected new-access, got %s", tok.AccessToken)
	}
	if store.acquireCalls != 1 {
		t.Errorf("expected 1 acquire call, got %d", store.acquireCalls)
	}
	if store.casCalls != 1 {
		t.Errorf("expected 1 CAS call, got %d", store.casCalls)
	}
	if broker.refreshCalls != 1 {
		t.Errorf("expected 1 broker refresh call, got %d", broker.refreshCalls)
	}
}

// TestDistributedRefresh_LeaseNotAcquired verifies that when the lease is held
// by another pod, the broker is NOT called and we fall back to store.Get.
func TestDistributedRefresh_LeaseNotAcquired(t *testing.T) {
	freshExpiry := time.Now().Add(1 * time.Hour)
	store := &mockTokenStore{
		acquireFunc: func(_ context.Context, _, _, _ string, _ time.Duration) (int64, bool, error) {
			return 0, false, nil
		},
		getFunc: func(_ context.Context, _, _ string) (*OAuthToken, error) {
			return &OAuthToken{
				Username:     "user1",
				Provider:     "google",
				AccessToken:  "other-pod-access",
				RefreshToken: "other-pod-refresh",
				ExpiresAt:    freshExpiry,
			}, nil
		},
	}

	broker := &mockBroker{}

	mgr := NewManager(
		WithTokenStore(store),
		WithBroker(broker),
		WithInstanceID("host:1:uuid-1"),
		WithLeaseTTL(30*time.Second),
	)

	tok, err := mgr.serializedRefresh(context.Background(), testKey, "old-refresh")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tok == nil {
		t.Fatal("expected non-nil token from store re-read")
	}
	if tok.AccessToken != "other-pod-access" {
		t.Errorf("expected other-pod-access, got %s", tok.AccessToken)
	}
	if broker.refreshCalls != 0 {
		t.Errorf("broker should NOT have been called, got %d calls", broker.refreshCalls)
	}
}

// TestDistributedRefresh_CASFails verifies that when CASPut returns false (version
// mismatch), we re-read from store and use that token.
func TestDistributedRefresh_CASFails(t *testing.T) {
	store := &mockTokenStore{
		acquireFunc: func(_ context.Context, _, _, _ string, _ time.Duration) (int64, bool, error) {
			return 1, true, nil
		},
		casFunc: func(_ context.Context, _ *OAuthToken, _ int64, _ string) (bool, error) {
			return false, nil // version mismatch
		},
		getFunc: func(_ context.Context, _, _ string) (*OAuthToken, error) {
			return &OAuthToken{
				Username:     "user1",
				Provider:     "google",
				AccessToken:  "winner-access",
				RefreshToken: "winner-refresh",
				ExpiresAt:    time.Now().Add(1 * time.Hour),
			}, nil
		},
	}

	broker := &mockBroker{
		refreshFunc: func(_ context.Context, _ Key, _ string) (*scyauth.Token, error) {
			return freshToken("my-access", "my-refresh", time.Now().Add(1*time.Hour)), nil
		},
	}

	mgr := NewManager(
		WithTokenStore(store),
		WithBroker(broker),
		WithInstanceID("host:1:uuid-1"),
	)

	tok, err := mgr.serializedRefresh(context.Background(), testKey, "old-refresh")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tok == nil {
		t.Fatal("expected non-nil token")
	}
	// Should get the winner's token from store re-read.
	if tok.AccessToken != "winner-access" {
		t.Errorf("expected winner-access, got %s", tok.AccessToken)
	}
}

// TestDistributedRefresh_BrokerError verifies that when the broker returns an error,
// the lease is released so another pod can retry.
func TestDistributedRefresh_BrokerError(t *testing.T) {
	store := &mockTokenStore{
		acquireFunc: func(_ context.Context, _, _, _ string, _ time.Duration) (int64, bool, error) {
			return 1, true, nil
		},
	}

	brokerErr := errors.New("oauth2: token revoked")
	broker := &mockBroker{
		refreshFunc: func(_ context.Context, _ Key, _ string) (*scyauth.Token, error) {
			return nil, brokerErr
		},
	}

	mgr := NewManager(
		WithTokenStore(store),
		WithBroker(broker),
		WithInstanceID("host:1:uuid-1"),
	)

	_, err := mgr.serializedRefresh(context.Background(), testKey, "old-refresh")
	if err == nil {
		t.Fatal("expected error from broker")
	}
	if !errors.Is(err, brokerErr) {
		t.Errorf("expected broker error, got: %v", err)
	}
	if store.releaseCalls != 1 {
		t.Errorf("expected 1 release call, got %d", store.releaseCalls)
	}
}

// TestDistributedRefresh_FallbackToLocal verifies that when instanceID is empty,
// the original serializedRefresh code path is used (no lease/CAS calls).
func TestDistributedRefresh_FallbackToLocal(t *testing.T) {
	store := &mockTokenStore{}

	broker := &mockBroker{
		refreshFunc: func(_ context.Context, _ Key, _ string) (*scyauth.Token, error) {
			return freshToken("local-access", "local-refresh", time.Now().Add(1*time.Hour)), nil
		},
	}

	mgr := NewManager(
		WithTokenStore(store),
		WithBroker(broker),
		WithInstanceID(""), // explicitly disable distributed mode
	)

	tok, err := mgr.serializedRefresh(context.Background(), testKey, "old-refresh")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tok == nil || tok.AccessToken != "local-access" {
		t.Errorf("expected local-access, got %v", tok)
	}
	if store.acquireCalls != 0 {
		t.Errorf("lease should NOT have been acquired in single-instance mode, got %d calls", store.acquireCalls)
	}
	if store.putCalls != 1 {
		t.Errorf("expected 1 Put call (local mode), got %d", store.putCalls)
	}
}

// TestDistributedRefresh_ExpiredLeaseTakeover verifies that when a lease has expired
// (e.g. from a dead pod), a new pod can acquire it.
func TestDistributedRefresh_ExpiredLeaseTakeover(t *testing.T) {
	store := &mockTokenStore{
		acquireFunc: func(_ context.Context, _, _, _ string, _ time.Duration) (int64, bool, error) {
			// Simulates expired lease: the SQL WHERE clause with lease_until < NOW() matches.
			return 5, true, nil
		},
		casFunc: func(_ context.Context, _ *OAuthToken, ver int64, _ string) (bool, error) {
			if ver != 5 {
				t.Errorf("expected version 5, got %d", ver)
			}
			return true, nil
		},
	}

	broker := &mockBroker{
		refreshFunc: func(_ context.Context, _ Key, _ string) (*scyauth.Token, error) {
			return freshToken("takeover-access", "takeover-refresh", time.Now().Add(1*time.Hour)), nil
		},
	}

	mgr := NewManager(
		WithTokenStore(store),
		WithBroker(broker),
		WithInstanceID("new-pod:2:uuid-2"),
	)

	tok, err := mgr.serializedRefresh(context.Background(), testKey, "dead-pod-refresh")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tok == nil || tok.AccessToken != "takeover-access" {
		t.Errorf("expected takeover-access, got %v", tok)
	}
	if store.acquireCalls != 1 {
		t.Errorf("expected 1 acquire call, got %d", store.acquireCalls)
	}
	if store.casCalls != 1 {
		t.Errorf("expected 1 CAS call, got %d", store.casCalls)
	}
}

// TestAutoDetectInstanceID verifies that NewManager auto-generates an InstanceID
// when a TokenStore is provided and no explicit InstanceID is set.
func TestAutoDetectInstanceID(t *testing.T) {
	store := &mockTokenStore{
		acquireFunc: func(_ context.Context, _, _, _ string, _ time.Duration) (int64, bool, error) {
			return 1, true, nil
		},
		casFunc: func(_ context.Context, _ *OAuthToken, _ int64, _ string) (bool, error) {
			return true, nil
		},
	}

	broker := &mockBroker{
		refreshFunc: func(_ context.Context, _ Key, _ string) (*scyauth.Token, error) {
			return freshToken("auto-access", "auto-refresh", time.Now().Add(1*time.Hour)), nil
		},
	}

	// No WithInstanceID — should auto-generate because store is present.
	mgr := NewManager(
		WithTokenStore(store),
		WithBroker(broker),
	)

	if mgr.instanceID == "" {
		t.Fatal("expected auto-generated instanceID when store is present")
	}

	tok, err := mgr.serializedRefresh(context.Background(), testKey, "old-refresh")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tok == nil || tok.AccessToken != "auto-access" {
		t.Errorf("expected auto-access, got %v", tok)
	}
	// Should use distributed path (acquire + CAS), not local Put.
	if store.acquireCalls != 1 {
		t.Errorf("expected 1 acquire call (auto-detect distributed), got %d", store.acquireCalls)
	}
	if store.casCalls != 1 {
		t.Errorf("expected 1 CAS call, got %d", store.casCalls)
	}
	if store.putCalls != 0 {
		t.Errorf("expected 0 Put calls (distributed mode uses CAS), got %d", store.putCalls)
	}
}

// TestAutoDetectDisabled verifies that WithInstanceID("") explicitly disables auto-detection.
func TestAutoDetectDisabled(t *testing.T) {
	store := &mockTokenStore{}

	mgr := NewManager(
		WithTokenStore(store),
		WithInstanceID(""), // explicitly disable
	)

	if mgr.instanceID != "" {
		t.Errorf("expected empty instanceID when explicitly disabled, got %q", mgr.instanceID)
	}
}
