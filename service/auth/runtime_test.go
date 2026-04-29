package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
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
