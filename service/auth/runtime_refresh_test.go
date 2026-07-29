package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	scyauth "github.com/viant/scy/auth"
	"golang.org/x/oauth2"
)

func TestTokenRefreshWatchInterval(t *testing.T) {
	const lead = 40 * time.Minute
	const defaultInterval = lead / 2

	t.Run("unset", func(t *testing.T) {
		previous, wasSet := os.LookupEnv(authRefreshWatchIntervalEnv)
		if err := os.Unsetenv(authRefreshWatchIntervalEnv); err != nil {
			t.Fatalf("unset %s: %v", authRefreshWatchIntervalEnv, err)
		}
		t.Cleanup(func() {
			if wasSet {
				_ = os.Setenv(authRefreshWatchIntervalEnv, previous)
				return
			}
			_ = os.Unsetenv(authRefreshWatchIntervalEnv)
		})

		got, overrideActive := tokenRefreshWatchInterval(lead)
		if got != defaultInterval {
			t.Fatalf("interval = %s, want default %s", got, defaultInterval)
		}
		if overrideActive {
			t.Fatal("override should not be active when environment variable is unset")
		}
	})

	t.Run("valid", func(t *testing.T) {
		t.Setenv(authRefreshWatchIntervalEnv, "750ms")

		got, overrideActive := tokenRefreshWatchInterval(lead)
		if got != 750*time.Millisecond {
			t.Fatalf("interval = %s, want 750ms", got)
		}
		if !overrideActive {
			t.Fatal("override should be active for a valid duration")
		}
	})

	t.Run("invalid", func(t *testing.T) {
		t.Setenv(authRefreshWatchIntervalEnv, "soon")

		got, overrideActive := tokenRefreshWatchInterval(lead)
		if got != defaultInterval {
			t.Fatalf("interval = %s, want default %s", got, defaultInterval)
		}
		if overrideActive {
			t.Fatal("override should not be active for an invalid duration")
		}
	})

	for _, value := range []string{"0s", "-1s"} {
		t.Run("non-positive "+value, func(t *testing.T) {
			t.Setenv(authRefreshWatchIntervalEnv, value)

			got, overrideActive := tokenRefreshWatchInterval(lead)
			if got != defaultInterval {
				t.Fatalf("interval = %s, want default %s", got, defaultInterval)
			}
			if overrideActive {
				t.Fatalf("override should not be active for %s", value)
			}
		})
	}
}

// recordingTokenStore embeds the minimal TokenStore behaviour we need for the
// invalidate-flow tests: it records Delete calls so assertions can verify the
// stale persistent row was removed on permanent refresh failure.
type recordingTokenStore struct {
	testTokenStore
	deletes atomic.Int32
	lastUsr string
	lastPrv string
}

func (s *recordingTokenStore) Delete(_ context.Context, username, provider string) error {
	s.deletes.Add(1)
	s.lastUsr = username
	s.lastPrv = provider
	return nil
}

type runtimeRefreshTestStore struct {
	testTokenStore
	acquireErr  error
	releaseErr  error
	putErr      error
	acquired    bool
	putCalls    atomic.Int32
	deleteCalls atomic.Int32
}

func (s *runtimeRefreshTestStore) Put(_ context.Context, _ *OAuthToken) error {
	s.putCalls.Add(1)
	return s.putErr
}

func (s *runtimeRefreshTestStore) Delete(_ context.Context, _, _ string) error {
	s.deleteCalls.Add(1)
	s.token = nil
	return nil
}

func (s *runtimeRefreshTestStore) TryAcquireRefreshLease(_ context.Context, _, _, _ string, _ time.Duration) (int64, bool, error) {
	return 1, s.acquired, s.acquireErr
}

func (s *runtimeRefreshTestStore) ReleaseRefreshLease(_ context.Context, _, _, _ string) error {
	return s.releaseErr
}

func writeRuntimeRefreshOAuthConfig(t *testing.T, tokenURL string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "oauth.json")
	data, err := json.Marshal(map[string]any{
		"authURL":      "https://idp.example.test/auth",
		"tokenURL":     tokenURL,
		"clientID":     "runtime-client",
		"clientSecret": "runtime-secret",
		"authStyle":    "header",
	})
	if err != nil {
		t.Fatalf("marshal oauth config: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write oauth config: %v", err)
	}
	return path
}

func runtimeRefreshHarness(configURL string, client *OAuthClient, store TokenStore) (*Runtime, *Session) {
	if client == nil {
		client = &OAuthClient{}
	}
	client.ConfigURL = configURL
	cfg := &Config{OAuth: &OAuth{Name: "oauth", Mode: "bff", Client: client}}
	sessions := NewManager(time.Hour, nil)
	runtime := &Runtime{
		cfg:      cfg,
		sessions: sessions,
		ext: &authExtension{
			cfg:        cfg,
			sessions:   sessions,
			tokenStore: store,
		},
	}
	sess := &Session{
		ID:       "session-refresh",
		UserID:   "user-42",
		Subject:  "provider-subject",
		Provider: "oauth",
		Tokens: &scyauth.Token{
			Token: oauth2.Token{
				AccessToken:  "old-access",
				RefreshToken: "old-refresh",
				Expiry:       time.Now().Add(-time.Minute),
			},
			IDToken: "old-id",
		},
	}
	sessions.Put(context.Background(), sess)
	return runtime, sess
}

func TestIsPermanentRefreshError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"plain error", errors.New("network issue"), false},
		{"context canceled", context.Canceled, false},
		{"context deadline exceeded", context.DeadlineExceeded, false},
		{"wrapped context canceled", errors.Join(errors.New("token endpoint"), context.Canceled), false},
		{"invalid_grant code", &oauth2.RetrieveError{ErrorCode: "invalid_grant"}, true},
		{"invalid_client", &oauth2.RetrieveError{ErrorCode: "invalid_client"}, false},
		{"invalid_token code (upper)", &oauth2.RetrieveError{ErrorCode: "INVALID_TOKEN"}, false},
		{"invalid_request", &oauth2.RetrieveError{ErrorCode: "invalid_request"}, false},
		{"unauthorized_client", &oauth2.RetrieveError{ErrorCode: "unauthorized_client"}, false},
		{"unsupported_grant_type", &oauth2.RetrieveError{ErrorCode: "unsupported_grant_type"}, false},
		{"400 with no code", &oauth2.RetrieveError{Response: &http.Response{StatusCode: http.StatusBadRequest}}, false},
		{"401 with no code", &oauth2.RetrieveError{Response: &http.Response{StatusCode: http.StatusUnauthorized}}, false},
		{"500 transient", &oauth2.RetrieveError{Response: &http.Response{StatusCode: http.StatusInternalServerError}}, false},
		{"500 invalid_grant preserves", &oauth2.RetrieveError{ErrorCode: "invalid_grant", Response: &http.Response{StatusCode: http.StatusInternalServerError}}, false},
		{"timeout transient", &oauth2.RetrieveError{Response: &http.Response{StatusCode: http.StatusGatewayTimeout}}, false},
		{"server_error code", &oauth2.RetrieveError{ErrorCode: "server_error", Response: &http.Response{StatusCode: http.StatusInternalServerError}}, false},
	}
	for _, tc := range cases {
		if got := isPermanentRefreshError(tc.err); got != tc.want {
			t.Errorf("%s: isPermanentRefreshError = %v, want %v (err=%v)", tc.name, got, tc.want, tc.err)
		}
	}
}

func TestRuntimeRefresh_InvalidGrantClearsEveryOtherFailurePreserves(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		contentType string
		body        string
		wantClear   bool
	}{
		{name: "invalid grant", status: http.StatusBadRequest, contentType: "application/json", body: `{"error":"invalid_grant"}`, wantClear: true},
		{name: "2xx invalid grant", status: http.StatusOK, contentType: "application/json", body: `{"error":"invalid_grant"}`, wantClear: true},
		{name: "invalid client", status: http.StatusUnauthorized, contentType: "application/json", body: `{"error":"invalid_client"}`},
		{name: "invalid token", status: http.StatusBadRequest, contentType: "application/json", body: `{"error":"invalid_token"}`},
		{name: "unauthorized client", status: http.StatusBadRequest, contentType: "application/json", body: `{"error":"unauthorized_client"}`},
		{name: "invalid request", status: http.StatusBadRequest, contentType: "application/json", body: `{"error":"invalid_request"}`},
		{name: "unsupported grant", status: http.StatusBadRequest, contentType: "application/json", body: `{"error":"unsupported_grant_type"}`},
		{name: "bare 400", status: http.StatusBadRequest, contentType: "text/plain", body: ""},
		{name: "bare 401", status: http.StatusUnauthorized, contentType: "text/plain", body: ""},
		{name: "server error", status: http.StatusServiceUnavailable, contentType: "application/json", body: `{"error":"server_error"}`},
		{name: "server invalid grant", status: http.StatusInternalServerError, contentType: "application/json", body: `{"error":"invalid_grant"}`},
		{name: "parse error", status: http.StatusOK, contentType: "application/json", body: `not-json`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", test.contentType)
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			store := &runtimeRefreshTestStore{acquired: true}
			runtime, sess := runtimeRefreshHarness(writeRuntimeRefreshOAuthConfig(t, server.URL), nil, store)

			got := runtime.tryRefreshToken(context.Background(), sess)
			if test.wantClear {
				if got.state != tokenConfirmedMissing {
					t.Fatalf("tryRefreshToken() state = %v, want confirmed missing", got.state)
				}
				if sess.Tokens != nil {
					t.Fatalf("invalid_grant tokens = %#v, want cleared", sess.Tokens)
				}
				if got := store.deleteCalls.Load(); got != 1 {
					t.Fatalf("Delete calls = %d, want 1", got)
				}
				return
			}
			if got.state != tokenPreserveWithoutInjection {
				t.Fatalf("tryRefreshToken() state = %v, want preserve without injection", got.state)
			}
			if sess.Tokens == nil || sess.Tokens.AccessToken != "old-access" || sess.Tokens.RefreshToken != "old-refresh" {
				t.Fatalf("preserved tokens = %#v", sess.Tokens)
			}
			if got := store.deleteCalls.Load(); got != 0 {
				t.Fatalf("Delete calls = %d, want 0", got)
			}
			if retryAt := runtime.loadRefreshRetryAt(sess); retryAt.IsZero() {
				t.Fatal("preserved failure did not enter cooldown")
			}
		})
	}
}

func TestRuntimeRefresh_LeaseAndPersistenceFailuresPreserve(t *testing.T) {
	t.Run("lease", func(t *testing.T) {
		store := &runtimeRefreshTestStore{acquireErr: errors.New("lease unavailable")}
		runtime, sess := runtimeRefreshHarness("unused", nil, store)
		if got := runtime.tryRefreshToken(context.Background(), sess); got.state != tokenPreserveWithoutInjection {
			t.Fatalf("tryRefreshToken() state = %v, want preserve without injection", got.state)
		}
		if sess.Tokens == nil || sess.Tokens.AccessToken != "old-access" {
			t.Fatalf("lease failure replaced tokens: %#v", sess.Tokens)
		}
		if runtime.loadRefreshRetryAt(sess).IsZero() {
			t.Fatal("lease failure did not enter cooldown")
		}
	})

	t.Run("persistence", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"candidate-access","refresh_token":"candidate-refresh","expires_in":3600}`))
		}))
		defer server.Close()
		store := &runtimeRefreshTestStore{acquired: true, putErr: errors.New("persist unavailable")}
		runtime, sess := runtimeRefreshHarness(writeRuntimeRefreshOAuthConfig(t, server.URL), nil, store)
		if got := runtime.tryRefreshToken(context.Background(), sess); got.state != tokenPreserveWithoutInjection {
			t.Fatalf("tryRefreshToken() state = %v, want preserve without injection", got.state)
		}
		if sess.Tokens == nil || sess.Tokens.AccessToken != "old-access" {
			t.Fatalf("persistence failure replaced tokens: %#v", sess.Tokens)
		}
		if got := store.putCalls.Load(); got != 1 {
			t.Fatalf("Put calls = %d, want 1", got)
		}
		if runtime.loadRefreshRetryAt(sess).IsZero() {
			t.Fatal("persistence failure did not enter cooldown")
		}
	})
}

func TestRuntimeRefresh_ScopeFailedCandidateIsNotStoredOrInjected(t *testing.T) {
	underScoped := fakeJWTWithClaims(t, map[string]any{"scope": "openid"})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  underScoped,
			"refresh_token": "candidate-refresh",
			"expires_in":    3600,
			"scope":         "openid",
		})
	}))
	defer server.Close()
	store := &runtimeRefreshTestStore{acquired: true}
	client := &OAuthClient{Scopes: []string{"openid", "ROLE_STEWARD_WEB"}}
	runtime, sess := runtimeRefreshHarness(writeRuntimeRefreshOAuthConfig(t, server.URL), client, store)

	if got := runtime.tryRefreshToken(context.Background(), sess); got.state != tokenPreserveWithoutInjection {
		t.Fatalf("scope-failed candidate state = %v, want preserve without injection", got.state)
	}
	if sess.Tokens == nil || sess.Tokens.AccessToken != "old-access" || sess.Tokens.RefreshToken != "old-refresh" {
		t.Fatalf("scope-failed candidate replaced session: %#v", sess.Tokens)
	}
	if got := store.putCalls.Load(); got != 0 {
		t.Fatalf("scope-failed candidate Put calls = %d, want 0", got)
	}
	if runtime.loadRefreshRetryAt(sess).IsZero() {
		t.Fatal("scope failure did not enter cooldown")
	}
}

// TestInvalidateSessionTokens_ClearsMemoryAndStore verifies that permanent
// refresh failure handling (a) wipes session tokens in memory so the next
// request doesn't retry the dead credential, and (b) deletes the persistent
// token row so a restart can't hydrate the dead tokens back into place.
func TestInvalidateSessionTokens_ClearsMemoryAndStore(t *testing.T) {
	store := &recordingTokenStore{}
	sessions := NewManager(0, nil)
	r := &Runtime{
		cfg:      &Config{},
		sessions: sessions,
		ext: &authExtension{
			cfg:        &Config{OAuth: &OAuth{Name: "oauth"}},
			sessions:   sessions,
			tokenStore: store,
		},
	}
	sess := &Session{
		ID:       "sess-x",
		Subject:  "user-sub",
		Provider: "oauth",
		Tokens: &scyauth.Token{
			Token: oauth2.Token{
				AccessToken:  "expired",
				RefreshToken: "dead-refresh",
				Expiry:       time.Now().Add(-time.Minute),
			},
			IDToken: "stale-id",
		},
	}
	sessions.Put(context.Background(), sess)

	r.invalidateSessionTokens(context.Background(), sess, "user-42", "oauth")

	if sess.Tokens != nil {
		t.Fatalf("session tokens should be nil after invalidation, got %#v", sess.Tokens)
	}
	if got := store.deletes.Load(); got != 1 {
		t.Fatalf("expected exactly one token-store Delete, got %d", got)
	}
	if store.lastUsr != "user-42" || store.lastPrv != "oauth" {
		t.Fatalf("Delete called with (%q, %q), want (user-42, oauth)", store.lastUsr, store.lastPrv)
	}
}

// TestInvalidateSessionTokens_NilStoreIsSafe guards against a nil token store
// — some deployments configure sessions without the persistent oauth token
// store and the invalidate path must still wipe memory without panicking.
func TestInvalidateSessionTokens_NilStoreIsSafe(t *testing.T) {
	sessions := NewManager(0, nil)
	r := &Runtime{cfg: &Config{}, sessions: sessions, ext: &authExtension{cfg: &Config{}, sessions: sessions}}
	sess := &Session{ID: "s1", Tokens: &scyauth.Token{Token: oauth2.Token{RefreshToken: "x"}}}
	r.invalidateSessionTokens(context.Background(), sess, "u", "p")
	if sess.Tokens != nil {
		t.Fatalf("expected tokens cleared, got %#v", sess.Tokens)
	}
}

type canceledCtxTokenStore struct {
	gotCtxCanceled bool
	putCtxCanceled bool
	token          *OAuthToken
}

func (s *canceledCtxTokenStore) Get(ctx context.Context, _, _ string) (*OAuthToken, error) {
	s.gotCtxCanceled = ctx.Err() != nil
	return s.token, nil
}

func (s *canceledCtxTokenStore) Put(ctx context.Context, _ *OAuthToken) error {
	s.putCtxCanceled = ctx.Err() != nil
	return nil
}

func (s *canceledCtxTokenStore) Delete(_ context.Context, _, _ string) error { return nil }
func (s *canceledCtxTokenStore) TryAcquireRefreshLease(_ context.Context, _, _, _ string, _ time.Duration) (int64, bool, error) {
	return 0, false, nil
}
func (s *canceledCtxTokenStore) ReleaseRefreshLease(_ context.Context, _, _, _ string) error {
	return nil
}
func (s *canceledCtxTokenStore) CASPut(_ context.Context, _ *OAuthToken, _ int64, _ string) (bool, error) {
	return false, nil
}

func TestTryLoadFreshTokenFromStore_IgnoresCanceledCallerContext(t *testing.T) {
	store := &canceledCtxTokenStore{
		token: &OAuthToken{
			Username:     "user-42",
			Provider:     "oauth",
			AccessToken:  "fresh-access",
			RefreshToken: "refresh",
			IDToken:      "fresh-id",
			ExpiresAt:    time.Now().Add(time.Hour),
		},
	}
	rt := &Runtime{
		ext: &authExtension{
			tokenStore: store,
			users: &testUserService{userBySubjectProvider: map[string]*User{
				"user-sub|oauth": {ID: "user-42", Username: "awitas"},
			}},
		},
		sessions: NewManager(time.Hour, nil),
	}
	sess := &Session{
		ID:       "sess-fresh",
		Subject:  "user-sub",
		Provider: "oauth",
		Tokens: &scyauth.Token{
			Token:   oauth2.Token{AccessToken: "stale", Expiry: time.Now().Add(-time.Minute)},
			IDToken: "stale-id",
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result := rt.tryLoadFreshTokenFromStore(ctx, sess)
	if result.state != tokenAvailable || result.token == nil || result.token.AccessToken != "fresh-access" {
		t.Fatalf("expected fresh token, got %#v", result)
	}
	if store.gotCtxCanceled {
		t.Fatalf("token store Get should not receive a canceled context")
	}
}
