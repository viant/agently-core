package auth

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRuntimeHandleCreateSession_UsesBearerTokenIdentityWhenBodyTokensMissing(t *testing.T) {
	ext := &authExtension{
		cfg: &Config{
			CookieName: "agently_session",
		},
		sessions: NewManager(0, nil),
	}

	claims := `{"sub":"user-123","email":"dev@example.com","preferred_username":"devuser"}`
	token := "x." + base64.RawURLEncoding.EncodeToString([]byte(claims)) + ".y"

	req := httptest.NewRequest(http.MethodPost, "/v1/api/auth/session", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	ext.handleCreateSession().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	resp := rec.Result()
	defer resp.Body.Close()
	var sessionCookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "agently_session" {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil || strings.TrimSpace(sessionCookie.Value) == "" {
		t.Fatalf("expected agently_session cookie to be set")
	}

	sess := ext.sessions.Get(req.Context(), strings.TrimSpace(sessionCookie.Value))
	if sess == nil {
		t.Fatalf("expected session to be stored")
	}
	if got := strings.TrimSpace(sess.Username); got != "devuser" {
		t.Fatalf("Username = %q, want %q", got, "devuser")
	}
	if got := strings.TrimSpace(sess.Subject); got != "user-123" {
		t.Fatalf("Subject = %q, want %q", got, "user-123")
	}
	if got := strings.TrimSpace(sess.Email); got != "dev@example.com" {
		t.Fatalf("Email = %q, want %q", got, "dev@example.com")
	}
	if sess.Tokens == nil || strings.TrimSpace(sess.Tokens.IDToken) != token {
		t.Fatalf("expected ID token to be captured from bearer token")
	}
}

func TestRuntimeHandleCreateSession_RejectsNonPOST(t *testing.T) {
	ext := &authExtension{
		cfg: &Config{
			CookieName: "agently_session",
		},
		sessions: NewManager(0, nil),
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/api/auth/session", nil)
	rec := httptest.NewRecorder()

	ext.handleCreateSession().ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusMethodNotAllowed, rec.Body.String())
	}
	if got := rec.Header().Get("Allow"); got != http.MethodPost {
		t.Fatalf("Allow = %q, want %q", got, http.MethodPost)
	}
}
