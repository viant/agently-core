package auth

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// Simulates another successful rotation/login between the initial durable read
// and acquisition of its released distributed lease. No database is used.
type rotateOnClaimStore struct {
	runtimeRefreshTestStore
	replacement *OAuthToken
}

func (s *rotateOnClaimStore) TryAcquireRefreshLease(context.Context, string, string, string, time.Duration) (int64, bool, error) {
	s.token = s.replacement
	return 2, true, nil
}
func TestOAuthRuntimeRefreshRechecksCredentialAfterAcquiringLease(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	defer server.Close()
	store := &rotateOnClaimStore{replacement: &OAuthToken{Username: "user-42", Provider: "oauth", AccessToken: "new-access", RefreshToken: "new-refresh", IDToken: "new-id", ExpiresAt: time.Now().Add(time.Hour)}}
	store.token = &OAuthToken{Username: "user-42", Provider: "oauth", AccessToken: "old-access", RefreshToken: "old-refresh", IDToken: "old-id", ExpiresAt: time.Now().Add(-time.Minute)}
	runtime, sess := runtimeRefreshHarness(writeRuntimeRefreshOAuthConfig(t, server.URL), nil, store)
	runtime.tryRefreshToken(context.Background(), sess)
	if calls.Load() != 0 || store.deleteCalls.Load() != 0 || sess.Tokens == nil || sess.Tokens.RefreshToken != "new-refresh" {
		t.Fatalf("stale credential was used or newer rotation erased: requests=%d deletes=%d sessionPresent=%t", calls.Load(), store.deleteCalls.Load(), sess.Tokens != nil)
	}
}

type refreshConflictStore struct {
	runtimeRefreshTestStore
	casCalls   atomic.Int32
	clearCalls atomic.Int32
}

func (s *refreshConflictStore) CASPut(context.Context, *OAuthToken, int64, string) (bool, error) {
	s.casCalls.Add(1)
	return false, nil
}
func (s *refreshConflictStore) CASClear(context.Context, string, string, int64, string) (bool, error) {
	s.clearCalls.Add(1)
	return false, nil
}
func newerRefreshFixture() *OAuthToken {
	return &OAuthToken{Username: "user-42", Provider: "oauth", AccessToken: "winner-access", RefreshToken: "winner-refresh", IDToken: "winner-id", ExpiresAt: time.Now().Add(time.Hour)}
}
func TestOAuthRuntimeStaleInvalidGrantCannotDeleteConcurrentLogin(t *testing.T) {
	store := &refreshConflictStore{}
	store.acquired = true
	store.token = &OAuthToken{Username: "user-42", Provider: "oauth", AccessToken: "old-access", RefreshToken: "old-refresh", IDToken: "old-id", ExpiresAt: time.Now().Add(-time.Minute)}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		store.token = newerRefreshFixture()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	defer server.Close()
	runtime, sess := runtimeRefreshHarness(writeRuntimeRefreshOAuthConfig(t, server.URL), nil, store)
	runtime.tryRefreshToken(context.Background(), sess)
	if store.deleteCalls.Load() != 0 || store.token == nil || store.token.RefreshToken != "winner-refresh" || sess.Tokens == nil || sess.Tokens.RefreshToken != "winner-refresh" {
		t.Fatalf("stale invalid_grant erased concurrent login: deletes=%d storePresent=%t sessionPresent=%t", store.deleteCalls.Load(), store.token != nil, sess.Tokens != nil)
	}
}
func TestOAuthRuntimeRefreshCASConflictAdoptsWinner(t *testing.T) {
	store := &refreshConflictStore{}
	store.acquired = true
	store.token = &OAuthToken{Username: "user-42", Provider: "oauth", AccessToken: "old-access", RefreshToken: "old-refresh", IDToken: "old-id", ExpiresAt: time.Now().Add(-time.Minute)}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		store.token = newerRefreshFixture()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"candidate-access","refresh_token":"candidate-refresh","token_type":"Bearer","expires_in":3600}`))
	}))
	defer server.Close()
	runtime, sess := runtimeRefreshHarness(writeRuntimeRefreshOAuthConfig(t, server.URL), nil, store)
	runtime.tryRefreshToken(context.Background(), sess)
	if store.putCalls.Load() != 0 || store.casCalls.Load() != 1 || sess.Tokens == nil || sess.Tokens.RefreshToken != "winner-refresh" {
		t.Fatalf("stale refresh did not honor CAS winner: unguardedPuts=%d CAS=%d sessionPresent=%t", store.putCalls.Load(), store.casCalls.Load(), sess.Tokens != nil)
	}
}

// Hides optional invalidation support while preserving the original store API.
type noConditionalClearStore struct{ TokenStore }

func TestOAuthRuntimeUnsupportedConditionalClearPreservesCredential(t *testing.T) {
	store := &runtimeRefreshTestStore{acquired: true}
	base := &noConditionalClearStore{TokenStore: store}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	defer server.Close()
	runtime, sess := runtimeRefreshHarness(writeRuntimeRefreshOAuthConfig(t, server.URL), nil, base)
	store.seedRefreshFixture(sess)
	result := runtime.tryRefreshToken(context.Background(), sess)
	if result.state != tokenPreserveWithoutInjection || store.deleteCalls.Load() != 0 || sess.Tokens == nil || runtime.loadRefreshRetryAt(sess).IsZero() {
		t.Fatal("unsupported conditional clear erased or injected credential")
	}
}
func TestOAuthRuntimeGenuineInvalidGrantClearsMatchingLeaseOnly(t *testing.T) {
	store := &runtimeRefreshTestStore{acquired: true}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	defer server.Close()
	runtime, sess := runtimeRefreshHarness(writeRuntimeRefreshOAuthConfig(t, server.URL), nil, store)
	result := runtime.tryRefreshToken(context.Background(), sess)
	if result.state != tokenConfirmedMissing || store.deleteCalls.Load() != 1 || sess.Tokens != nil {
		t.Fatal("matching revoked credential retained or injected")
	}
}

func TestOAuthRuntimeWinnerWithNonIncreasingExpiryIsAdopted(t *testing.T) {
	for _, earlier := range []bool{false, true} {
		for _, success := range []bool{false, true} {
			t.Run(fmt.Sprintf("earlier=%v/success=%v", earlier, success), func(t *testing.T) {
				store := &refreshConflictStore{}
				store.acquired = true
				expiry := time.Now().Add(time.Hour)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					winner := newerRefreshFixture()
					winner.ExpiresAt = expiry
					if earlier {
						winner.ExpiresAt = expiry.Add(-time.Minute)
					}
					store.token = winner
					w.Header().Set("Content-Type", "application/json")
					if success {
						_, _ = w.Write([]byte(`{"access_token":"candidate-access","refresh_token":"candidate-refresh","token_type":"Bearer","expires_in":3600}`))
					} else {
						w.WriteHeader(http.StatusUnauthorized)
						_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
					}
				}))
				defer server.Close()
				runtime, sess := runtimeRefreshHarness(writeRuntimeRefreshOAuthConfig(t, server.URL), nil, store)
				sess.Tokens.Expiry = expiry
				store.token.ExpiresAt = expiry
				result := runtime.tryRefreshToken(context.Background(), sess)
				if result.state != tokenAvailable || sess.Tokens == nil || sess.Tokens.RefreshToken != "winner-refresh" || store.deleteCalls.Load() != 0 {
					t.Fatal("validated newer winner rejected by expiry-only ordering")
				}
			})
		}
	}
}
func TestOAuthRuntimePostClaimAdoptsNewAccessAndIDWithUnchangedRefresh(t *testing.T) {
	store := &rotateOnClaimStore{replacement: newerRefreshFixture()}
	store.replacement.RefreshToken = "old-refresh"
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	runtime, sess := runtimeRefreshHarness(writeRuntimeRefreshOAuthConfig(t, server.URL), nil, store)
	result := runtime.tryRefreshToken(context.Background(), sess)
	if calls.Load() != 0 || result.state != tokenAvailable || sess.Tokens.AccessToken != "winner-access" || sess.Tokens.IDToken != "winner-id" {
		t.Fatal("post-claim credential revision was ignored because refresh string matched")
	}
}

func TestOAuthRuntimeWinnerReloadKeepsOrdinaryExpiryGuardAndSecurityChecks(t *testing.T) {
	store := &runtimeRefreshTestStore{acquired: true}
	runtime, sess := runtimeRefreshHarness("unused", nil, store)
	expiry := time.Now().Add(time.Hour)
	sess.Tokens.Expiry = expiry
	store.token = newerRefreshFixture()
	store.token.ExpiresAt = expiry
	if result := runtime.tryLoadFreshTokenFromStore(context.Background(), sess); result.state != tokenPreserveWithoutInjection || sess.Tokens.AccessToken != "old-access" {
		t.Fatal("ordinary expiry guard was weakened")
	}
	if result := runtime.recoverRefreshWinner(context.Background(), sess); result.state != tokenAvailable || sess.Tokens.AccessToken != "winner-access" {
		t.Fatal("narrow validated winner was not adopted")
	}
	for _, scenario := range []string{"provider", "owner", "scope"} {
		t.Run(scenario, func(t *testing.T) {
			store := &runtimeRefreshTestStore{acquired: true}
			runtime, sess := runtimeRefreshHarness("unused", nil, store)
			sess.Tokens.Expiry = expiry
			winner := newerRefreshFixture()
			winner.ExpiresAt = expiry
			store.token = winner
			switch scenario {
			case "provider":
				winner.Provider = "foreign"
			case "owner":
				runtime.cfg.Enabled = true
				winner.Username = "foreign"
				winner.IDToken = fakeJWTWithClaims(t, map[string]any{"sub": sess.Subject, "iss": "test", "exp": expiry.Unix()})
			case "scope":
				runtime.ext.cfg.OAuth.Client.Scopes = []string{"openid", "ROLE_READER"}
				winner.Scopes = []string{"openid"}
			}
			if result := runtime.recoverRefreshWinner(context.Background(), sess); result.state == tokenAvailable || sess.Tokens.AccessToken == "winner-access" {
				t.Fatal("winner reload bypassed owned provider/identity/scope checks")
			}
		})
	}
}
