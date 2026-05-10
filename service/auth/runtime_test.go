package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	iauth "github.com/viant/agently-core/internal/auth"
	scyauth "github.com/viant/scy/auth"
	"golang.org/x/oauth2"
)

func TestWithRuntimeAuthUserBridgesCoreContexts(t *testing.T) {
	tokens := &scyauth.Token{}
	tokens.Token.AccessToken = "access-token"

	ctx := withRuntimeAuthUser(context.Background(), &runtimeAuthUser{
		Subject:  "devuser",
		Email:    "devuser@example.com",
		Provider: "oauth",
		Tokens:   tokens,
	})

	if got := EffectiveUserID(ctx); got != "devuser" {
		t.Fatalf("auth effective user = %q, want %q", got, "devuser")
	}
	if got := MCPAuthToken(ctx, false); got != "access-token" {
		t.Fatalf("auth MCP token = %q, want %q", got, "access-token")
	}
	if got := iauth.Provider(ctx); got != "oauth" {
		t.Fatalf("auth provider = %q, want %q", got, "oauth")
	}
}

func TestRuntime_EnsureDefaultUser_OAuthBFFDoesNotFallbackToDefaultUsername(t *testing.T) {
	rt := &Runtime{
		cfg: &Config{
			Enabled:         true,
			DefaultUsername: "devuser",
			CookieName:      "agently_session",
			Local:           &Local{Enabled: false},
			OAuth:           &OAuth{Mode: "bff"},
		},
		sessions: NewManager(0, nil),
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/conversations", nil)
	got := rt.ensureDefaultUser(rec, req)
	if got != nil {
		t.Fatalf("expected no default user in oauth bff mode, got %#v", got)
	}
}

func TestRuntime_EnsureDefaultUser_DoesNotBlockOnSessionPersistence(t *testing.T) {
	store := &blockingSessionStore{
		release: make(chan struct{}),
	}
	rt := &Runtime{
		cfg: &Config{
			Enabled:         true,
			DefaultUsername: "devuser",
			CookieName:      "agently_session",
			Local:           &Local{Enabled: true},
		},
		sessions: NewManager(0, store),
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/api/auth/me", nil)

	start := time.Now()
	got := rt.ensureDefaultUser(rec, req)
	elapsed := time.Since(start)
	if got == nil {
		t.Fatalf("expected default user, got nil")
	}
	if elapsed > 250*time.Millisecond {
		t.Fatalf("ensureDefaultUser blocked on session persistence for %s", elapsed)
	}

	deadline := time.Now().Add(1 * time.Second)
	for store.upserts == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if store.upserts == 0 {
		t.Fatalf("expected async session persistence to start")
	}

	cookies := rec.Result().Cookies()
	if len(cookies) == 0 || cookies[0].Name != "agently_session" || !strings.HasPrefix(cookies[0].Value, "auto-") {
		t.Fatalf("expected auto local session cookie, got %#v", cookies)
	}

	close(store.release)
}

func TestRefreshedOAuthIDToken_UsesNewIDTokenWhenPresent(t *testing.T) {
	refreshed := &oauth2.Token{}
	refreshed = refreshed.WithExtra(map[string]interface{}{
		"id_token": "fresh-id-token",
	})
	got := refreshedOAuthIDToken(refreshed, "stale-id-token")
	if got != "fresh-id-token" {
		t.Fatalf("refreshedOAuthIDToken() = %q, want %q", got, "fresh-id-token")
	}
}

func TestRefreshedOAuthIDToken_FallsBackToCurrentWhenMissing(t *testing.T) {
	refreshed := &oauth2.Token{}
	got := refreshedOAuthIDToken(refreshed, "stale-id-token")
	if got != "stale-id-token" {
		t.Fatalf("refreshedOAuthIDToken() = %q, want %q", got, "stale-id-token")
	}
}

func TestRuntimeProtect_MixedLocalAndOAuthAcceptsLocalSessionCookie(t *testing.T) {
	rt := &Runtime{
		cfg: &Config{
			Enabled:    true,
			CookieName: "agently_session",
			IpHashKey:  "dev-hmac-salt",
			Local:      &Local{Enabled: true},
			OAuth:      &OAuth{Mode: "bff"},
		},
		sessions: NewManager(0, nil),
	}
	rt.sessions.Put(nil, &Session{
		ID:       "sess-1",
		Username: "awitas",
		Subject:  "awitas",
		Provider: "local",
	})

	handler := rt.protect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/api/auth/me", nil)
	req.AddCookie(&http.Cookie{Name: "agently_session", Value: "sess-1"})
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
}

func TestRuntimeProtect_TransientRefreshFailureDoesNotDeleteSession(t *testing.T) {
	rt := &Runtime{
		cfg: &Config{
			Enabled:    true,
			CookieName: "agently_session",
			IpHashKey:  "dev-hmac-salt",
			OAuth:      &OAuth{Mode: "bff"},
		},
		sessions: NewManager(0, nil),
		ext: &authExtension{
			cfg: &Config{
				Enabled:    true,
				CookieName: "agently_session",
				OAuth: &OAuth{
					Mode: "bff",
				},
			},
		},
	}

	tokens := &scyauth.Token{}
	tokens.Token.AccessToken = "expired-access"
	tokens.Token.RefreshToken = "refresh-token"
	tokens.Token.Expiry = time.Now().Add(-1 * time.Minute)

	rt.sessions.Put(nil, &Session{
		ID:       "sess-expired",
		Username: "awitas_viant_devtest",
		Subject:  "awitas_viant_devtest",
		Provider: "oauth",
		Tokens:   tokens,
	})

	handler := rt.protectAll(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/conversations", nil)
	req.AddCookie(&http.Cookie{Name: "agently_session", Value: "sess-expired"})
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if got := rt.sessions.Get(context.Background(), "sess-expired"); got == nil {
		t.Fatalf("expected session to be preserved after transient refresh failure")
	} else if got.TransientRefreshRetryAt.IsZero() {
		t.Fatalf("expected preserved session to carry transient refresh cooldown")
	}
}

func TestRuntimeProtect_TransientRefreshCooldownSkipsRepeatedRefreshAttempts(t *testing.T) {
	rt := &Runtime{
		cfg: &Config{
			Enabled:    true,
			CookieName: "agently_session",
			IpHashKey:  "dev-hmac-salt",
			OAuth:      &OAuth{Mode: "bff"},
		},
		sessions: NewManager(0, nil),
	}

	tokens := &scyauth.Token{}
	tokens.Token.AccessToken = "expired-access"
	tokens.Token.RefreshToken = "refresh-token"
	tokens.Token.Expiry = time.Now().Add(-1 * time.Minute)

	rt.sessions.Put(nil, &Session{
		ID:                      "sess-expired-cooldown",
		Username:                "awitas_viant_devtest",
		Subject:                 "awitas_viant_devtest",
		Provider:                "oauth",
		Tokens:                  tokens,
		TransientRefreshRetryAt: time.Now().Add(30 * time.Second),
	})

	handler := rt.protectAll(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/conversations", nil)
	req.AddCookie(&http.Cookie{Name: "agently_session", Value: "sess-expired-cooldown"})
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	got := rt.sessions.Get(context.Background(), "sess-expired-cooldown")
	if got == nil {
		t.Fatalf("expected session to be preserved during transient refresh cooldown")
	}
	if got.Tokens == nil {
		t.Fatalf("expected expired tokens to remain during transient refresh cooldown")
	}
	if got.TransientRefreshRetryAt.IsZero() {
		t.Fatalf("expected transient refresh retry timestamp to remain set")
	}
}

func TestRuntimeProtect_ExpiredSessionRefreshHonorsRequestContext(t *testing.T) {
	var hits atomic.Int32
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		select {
		case <-r.Context().Done():
			return
		case <-time.After(300 * time.Millisecond):
			http.Error(w, "slow token endpoint", http.StatusGatewayTimeout)
		}
	}))
	defer tokenSrv.Close()

	cfgDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "oauth.json")
	payload := map[string]any{
		"authURL":      tokenSrv.URL + "/auth",
		"tokenURL":     tokenSrv.URL + "/token",
		"clientID":     "test-client",
		"clientSecret": "test-secret",
		"redirectURL":  "http://localhost/callback",
		"scopes":       []string{"openid"},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if err := os.WriteFile(cfgPath, raw, 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	rt := &Runtime{
		cfg: &Config{
			Enabled:    true,
			CookieName: "agently_session",
			IpHashKey:  "dev-hmac-salt",
			OAuth:      &OAuth{Mode: "bff"},
		},
		sessions: NewManager(0, nil),
	}
	rt.ext = &authExtension{
		cfg: &Config{
			Enabled:    true,
			CookieName: "agently_session",
			IpHashKey:  "dev-hmac-salt",
			OAuth: &OAuth{
				Name: "oauth",
				Mode: "bff",
				Client: &OAuthClient{
					ConfigURL: cfgPath,
				},
			},
		},
		sessions: rt.sessions,
	}

	tokens := &scyauth.Token{}
	tokens.Token.AccessToken = "expired-access"
	tokens.Token.RefreshToken = "refresh-token"
	tokens.Token.Expiry = time.Now().Add(-1 * time.Minute)
	tokens.IDToken = "expired-id"

	rt.sessions.Put(nil, &Session{
		ID:       "sess-refresh-timeout",
		Username: "awitas",
		Subject:  "awitas_viant_devtest",
		Provider: "oauth",
		Tokens:   tokens,
	})

	handler := rt.protect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/v1/api/auth/me", nil).WithContext(ctx)
	req.AddCookie(&http.Cookie{Name: "agently_session", Value: "sess-refresh-timeout"})
	rec := httptest.NewRecorder()

	started := time.Now()
	handler.ServeHTTP(rec, req)
	elapsed := time.Since(started)

	if got := hits.Load(); got == 0 {
		t.Fatalf("expected token refresh request to be attempted")
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("auth handler took %v, want it to honor request context timeout", elapsed)
	}
}

func TestRuntimeRefreshCooldown_PersistsAcrossSessionObjects(t *testing.T) {
	rt := &Runtime{}
	until := time.Now().Add(30 * time.Second).UTC()

	seed := &Session{
		ID:       "sess-a",
		Subject:  "awitas_viant_devtest",
		Provider: "oauth",
	}
	rt.storeRefreshRetryAt(seed, until)

	reloaded := &Session{
		ID:       "sess-b",
		Subject:  "awitas_viant_devtest",
		Provider: "oauth",
	}
	got := rt.loadRefreshRetryAt(reloaded)
	if got.IsZero() {
		t.Fatalf("expected non-zero retry timestamp")
	}
	if !got.Equal(until) {
		t.Fatalf("retry timestamp = %v, want %v", got, until)
	}
	if reloaded.TransientRefreshRetryAt.IsZero() || !reloaded.TransientRefreshRetryAt.Equal(until) {
		t.Fatalf("session retry timestamp = %v, want %v", reloaded.TransientRefreshRetryAt, until)
	}

	rt.clearRefreshRetryAt(reloaded)
	if got := rt.loadRefreshRetryAt(&Session{Subject: "awitas_viant_devtest", Provider: "oauth"}); !got.IsZero() {
		t.Fatalf("expected cleared retry timestamp, got %v", got)
	}
}

func TestRuntimeShouldLogRefreshRetry_LogsOncePerCooldownWindow(t *testing.T) {
	rt := &Runtime{}
	sess := &Session{Subject: "awitas_viant_devtest", Provider: "oauth"}
	until := time.Now().Add(30 * time.Second).UTC()

	if !rt.shouldLogRefreshRetry(sess, until) {
		t.Fatalf("expected first log allowance")
	}
	if rt.shouldLogRefreshRetry(sess, until) {
		t.Fatalf("expected duplicate cooldown log to be suppressed")
	}
	if !rt.shouldLogRefreshRetry(sess, until.Add(time.Second)) {
		t.Fatalf("expected new cooldown window to log again")
	}
	rt.clearRefreshRetryAt(sess)
	if !rt.shouldLogRefreshRetry(sess, until) {
		t.Fatalf("expected logging to reset after clear")
	}
}
