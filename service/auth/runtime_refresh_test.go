package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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
	lastPut     *OAuthToken
}

type storedRefreshUserService struct {
	*availabilityUserService
	user *User
}

func (s *storedRefreshUserService) GetByID(_ context.Context, id string) (*User, error) {
	if s.user != nil && s.user.ID == id {
		return s.user, nil
	}
	return nil, nil
}

func (s *storedRefreshUserService) GetBySubjectAndProvider(_ context.Context, subject, provider string) (*User, error) {
	if s.user != nil && s.user.Subject == subject && s.user.Provider == provider {
		return s.user, nil
	}
	return nil, nil
}

func (s *runtimeRefreshTestStore) Put(_ context.Context, token *OAuthToken) error {
	s.putCalls.Add(1)
	s.lastPut = token
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
	if fixture, ok := store.(interface{ seedRefreshFixture(*Session) }); ok {
		fixture.seedRefreshFixture(sess)
	}
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

func TestRuntimeRefresh_RotatedTokenDiscardedBeforeInvalidGrant(t *testing.T) {
	logs := captureStandardLog(t)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.FormValue("refresh_token"); got != "old-refresh" {
			t.Errorf("refresh token = %q, want old-refresh", got)
		}
		w.Header().Set("Content-Type", "application/json")
		if requests.Add(1) == 1 {
			_, _ = w.Write([]byte(`{"access_token":"candidate-access","refresh_token":"rotated-refresh","expires_in":3600}`))
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	defer server.Close()

	store := &runtimeRefreshTestStore{acquired: true}
	runtime, sess := runtimeRefreshHarness(writeRuntimeRefreshOAuthConfig(t, server.URL), nil, store)
	runtime.cfg.Enabled = true
	sess.Tokens.IDToken = fakeJWTWithClaims(t, map[string]any{
		"iss": "https://idp.example.test/", "sub": sess.Subject,
		"exp": time.Now().Add(-time.Minute).Unix(),
	})

	// The durable leased fixture represents the same expired credential as the session.
	store.token.IDToken = sess.Tokens.IDToken
	if got := runtime.tryRefreshToken(context.Background(), sess); got.state != tokenPreserveWithoutInjection {
		t.Fatalf("first refresh state = %v, want preserve without injection", got.state)
	}
	if got := refreshIdentityFailureReason(errors.New("fresh ID token required")); got != "fresh_id_token_required" {
		t.Fatalf("reason = %q", got)
	}
	if store.putCalls.Load() != 0 || sess.Tokens.RefreshToken != "old-refresh" {
		t.Fatalf("rotated refresh token was unexpectedly persisted or replaced")
	}
	if got := runtime.tryRefreshToken(context.Background(), sess); got.state != tokenConfirmedMissing {
		t.Fatalf("second refresh state = %v, want confirmed missing", got.state)
	}
	if requests.Load() != 2 || store.deleteCalls.Load() != 1 {
		t.Fatalf("requests = %d, deletes = %d, want 2 and 1", requests.Load(), store.deleteCalls.Load())
	}
	output := logs.String()
	if strings.Contains(output, `attempt_id=""`) {
		t.Fatalf("refresh diagnostic lacks an attempt ID: %q", output)
	}
	for _, expected := range []string{
		`op="refresh_response"`, `refresh_rotated="true"`,
		`op="refresh_identity_validate"`, `reason="fresh_id_token_required"`,
		`op="refresh_action"`, `oauth_error="invalid_grant"`,
		`op="store_delete"`, `action="deleted"`,
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("missing diagnostic %s in %q", expected, output)
		}
	}
	for _, secret := range []string{"old-refresh", "rotated-refresh", "candidate-access"} {
		if strings.Contains(output, secret) {
			t.Fatalf("refresh diagnostic leaked %s", secret)
		}
	}
}

func TestRuntimeRefresh_StoreScanUsesOIDCSubject(t *testing.T) {
	logs := captureStandardLog(t)
	authority := newSessionOAuthAuthority(t)
	const storedUserID = "internal-user-uuid"
	const oidcSubject = "provider-subject"
	claims := authority.claims("client")
	claims["sub"] = oidcSubject
	oldIDToken := authority.sign(t, claims)
	refreshedIDToken := authority.sign(t, claims)
	var requests atomic.Int32
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.FormValue("refresh_token"); got != "old-refresh" {
			t.Errorf("refresh token = %q, want the stored credential", got)
		}
		w.Header().Set("Content-Type", "application/json")
		if requests.Add(1) > 1 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "new-access", "refresh_token": "new-refresh",
			"id_token": refreshedIDToken, "expires_in": 3600, "scope": "read",
		})
	}))
	defer tokenServer.Close()

	store := &scanningRefreshStore{
		runtimeRefreshTestStore: &runtimeRefreshTestStore{acquired: true},
		scan: []*OAuthToken{{
			Username: storedUserID, Provider: "oauth", AccessToken: "old-access",
			RefreshToken: "old-refresh", IDToken: oldIDToken,
			ExpiresAt: time.Now().Add(-time.Minute),
		}},
	}
	store.token = store.scan[0]
	configURL := writeRuntimeRefreshOAuthConfig(t, tokenServer.URL)
	runtime, _ := runtimeRefreshHarness(configURL, authority.config(false).OAuth.Client, store)
	runtime.cfg.Enabled = true
	runtime.cfg.OAuth.Name = "oauth"
	runtime.cfg.OAuth.Client.Issuer = authority.server.URL
	runtime.cfg.OAuth.Client.JWKSURL = authority.server.URL + "/keys"
	runtime.cfg.OAuth.Client.ClientID = "client"
	runtime.ext.users = &storedRefreshUserService{
		availabilityUserService: &availabilityUserService{},
		user:                    &User{ID: storedUserID, Username: "test-user", Provider: "oauth", Subject: oidcSubject},
	}
	storeSession := &Session{Subject: storedUserID, Tokens: &scyauth.Token{IDToken: oldIDToken}}
	if reason := refreshIdentityFailureReason(validateOwnedSessionIDToken(context.Background(), runtime.cfg, storeSession, refreshedIDToken)); reason != "subject_mismatch" {
		t.Fatalf("validation reason = %q, want subject_mismatch", reason)
	}
	runtime.refreshTokenStore(context.Background())

	if requests.Load() != 1 || store.putCalls.Load() != 1 || store.deleteCalls.Load() != 0 {
		t.Fatalf("requests=%d puts=%d deletes=%d, want one successful refresh and no invalidation",
			requests.Load(), store.putCalls.Load(), store.deleteCalls.Load())
	}
	if store.lastPut == nil || store.lastPut.RefreshToken != "new-refresh" || store.lastPut.IDToken != refreshedIDToken {
		t.Fatal("rotated refresh credential and verified identity were not persisted together")
	}
	store.token = store.lastPut
	store.scan[0] = store.lastPut
	runtime.refreshTokenStore(context.Background())
	if requests.Load() != 1 {
		t.Fatalf("next scan retried a non-expiring credential: requests=%d", requests.Load())
	}
	for _, expected := range []string{`op="store_refresh_selected"`, `source="store_scan"`, `reason="different"`, `refresh_rotated="true"`, `op="refresh_store_put"`, `action="stored"`} {
		if !strings.Contains(logs.String(), expected) {
			t.Fatalf("missing diagnostic %s in %q", expected, logs.String())
		}
	}
	if strings.Contains(logs.String(), `reason="subject_mismatch"`) || strings.Contains(logs.String(), `oauth_error="invalid_grant"`) {
		t.Fatalf("successful refresh reported a mismatch or invalid grant: %q", logs.String())
	}
	for _, secret := range []string{"old-refresh", "new-refresh"} {
		if strings.Contains(logs.String(), secret) {
			t.Fatalf("refresh diagnostic leaked %s", secret)
		}
	}
}

func TestRuntimeRefresh_StoreScanSkipsMismatchedUserBeforeOAuth(t *testing.T) {
	for _, tc := range []struct {
		name   string
		user   *User
		reason string
	}{
		{name: "missing user", reason: "user_not_found"},
		{name: "disabled user", user: &User{ID: "internal-user-uuid", Provider: "oauth", Subject: "provider-subject", Disabled: true}, reason: "user_disabled"},
		{name: "wrong provider", user: &User{ID: "internal-user-uuid", Provider: "local", Subject: "provider-subject"}, reason: "provider_mismatch"},
		{name: "wrong subject", user: &User{ID: "internal-user-uuid", Provider: "oauth", Subject: "other-subject"}, reason: "subject_mismatch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logs := captureStandardLog(t)
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				requests.Add(1)
			}))
			defer server.Close()
			store := &scanningRefreshStore{
				runtimeRefreshTestStore: &runtimeRefreshTestStore{acquired: true},
				scan: []*OAuthToken{{
					Username: "internal-user-uuid", Provider: "oauth", RefreshToken: "old-refresh",
					IDToken:   fakeJWTWithClaims(t, map[string]any{"sub": "provider-subject"}),
					ExpiresAt: time.Now().Add(-time.Minute),
				}},
			}
			runtime, _ := runtimeRefreshHarness(writeRuntimeRefreshOAuthConfig(t, server.URL), nil, store)
			runtime.cfg.Enabled = true
			runtime.ext.users = &storedRefreshUserService{availabilityUserService: &availabilityUserService{}, user: tc.user}
			runtime.refreshTokenStore(context.Background())
			if requests.Load() != 0 || store.putCalls.Load() != 0 || store.deleteCalls.Load() != 0 {
				t.Fatalf("unowned credential was refreshed or modified: requests=%d puts=%d deletes=%d",
					requests.Load(), store.putCalls.Load(), store.deleteCalls.Load())
			}
			if !strings.Contains(logs.String(), `reason="`+tc.reason+`"`) {
				t.Fatalf("missing %s diagnostic in %q", tc.reason, logs.String())
			}
		})
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

func (s *runtimeRefreshTestStore) seedRefreshFixture(sess *Session) {
	if s.token == nil {
		s.token = &OAuthToken{Username: sess.UserID, Provider: sess.Provider, AccessToken: sess.Tokens.AccessToken, IDToken: sess.Tokens.IDToken, RefreshToken: sess.Tokens.RefreshToken, ExpiresAt: sess.Tokens.Expiry}
	}
}
func (s *runtimeRefreshTestStore) CASPut(ctx context.Context, token *OAuthToken, _ int64, _ string) (bool, error) {
	err := s.Put(ctx, token)
	if err == nil {
		s.token = token
	}
	return err == nil, err
}
func (s *runtimeRefreshTestStore) CASClear(ctx context.Context, user, provider string, _ int64, _ string) (bool, error) {
	return true, s.Delete(ctx, user, provider)
}
