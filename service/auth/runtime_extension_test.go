package auth

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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

func TestRuntimeHandleCreateSession_InferExpiryFromJWTWhenMissing(t *testing.T) {
	ext := &authExtension{
		cfg: &Config{
			CookieName: "agently_session",
			OAuth:      &OAuth{Name: "oauth", Mode: "bff"},
		},
		sessions: NewManager(time.Hour, nil),
	}

	exp := time.Now().Add(90 * time.Minute).UTC().Truncate(time.Second)
	claims := map[string]any{
		"sub":                "user-123",
		"email":              "dev@example.com",
		"preferred_username": "devuser",
		"exp":                exp.Unix(),
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	idToken := "x." + base64.RawURLEncoding.EncodeToString(payload) + ".y"

	req := httptest.NewRequest(http.MethodPost, "/v1/api/auth/session", strings.NewReader(
		`{"username":"devuser","idToken":"`+idToken+`","accessToken":"token-access"}`,
	))
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
	if sessionCookie == nil {
		t.Fatalf("expected agently_session cookie to be set")
	}

	sess := ext.sessions.Get(req.Context(), strings.TrimSpace(sessionCookie.Value))
	if sess == nil || sess.Tokens == nil {
		t.Fatalf("expected session tokens to be stored")
	}
	if !sess.Tokens.Expiry.Equal(exp) {
		t.Fatalf("Tokens.Expiry = %v, want %v", sess.Tokens.Expiry, exp)
	}
}

func TestRuntimeHandleCreateSession_BearerBootstrapDoesNotPersistOAuthToken(t *testing.T) {
	store := &testTokenStore{}
	ext := &authExtension{
		cfg: &Config{
			CookieName: "agently_session",
			OAuth:      &OAuth{Name: "oauth", Mode: "bff"},
		},
		sessions:   NewManager(time.Hour, nil),
		tokenStore: store,
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
	if store.putUser != "" {
		t.Fatalf("expected bearer bootstrap not to persist oauth token, got putUser=%q", store.putUser)
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

func TestRuntimeHandleMe_AllowsLocalSessionWhenOAuthBFFAlsoConfigured(t *testing.T) {
	ext := &authExtension{
		cfg: &Config{
			CookieName: "agently_session",
			Local:      &Local{Enabled: true},
			OAuth:      &OAuth{Name: "oauth", Mode: "bff"},
		},
		sessions: NewManager(time.Hour, nil),
	}

	sess := &Session{
		ID:        "sess-1",
		Username:  "awitas",
		Subject:   "awitas",
		Provider:  "local",
		CreatedAt: time.Now(),
	}
	ext.sessions.Put(nil, sess)

	req := httptest.NewRequest(http.MethodGet, "/v1/api/auth/me", nil)
	req.AddCookie(&http.Cookie{Name: "agently_session", Value: sess.ID})
	rec := httptest.NewRecorder()

	ext.handleMe().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if got := strings.TrimSpace(out["username"].(string)); got != "awitas" {
		t.Fatalf("username = %q, want %q", got, "awitas")
	}
	if got := strings.TrimSpace(out["provider"].(string)); got != "session" {
		t.Fatalf("provider = %q, want %q", got, "session")
	}
}

func TestRuntimeHandleMe_UsesStoredDisplayNameForOAuthSession(t *testing.T) {
	store := &testTokenStore{
		token: &OAuthToken{
			Username:     "user-42",
			Provider:     "oauth",
			AccessToken:  "access",
			RefreshToken: "refresh",
			IDToken:      "id",
			ExpiresAt:    time.Now().Add(time.Hour),
		},
	}
	ext := &authExtension{
		cfg: &Config{
			CookieName: "agently_session",
			OAuth:      &OAuth{Name: "oauth", Mode: "bff"},
		},
		sessions:   NewManager(time.Hour, nil),
		tokenStore: store,
		users: &testUserService{
			userBySubjectProvider: map[string]*User{
				"awitas_viant_devtest|oauth": {
					ID:          "user-42",
					Username:    "awitas",
					DisplayName: "Awitas",
				},
			},
		},
	}

	sess := &Session{
		ID:        "sess-1",
		Username:  "awitas",
		Subject:   "awitas_viant_devtest",
		Provider:  "oauth",
		CreatedAt: time.Now(),
	}
	ext.sessions.Put(nil, sess)

	req := httptest.NewRequest(http.MethodGet, "/v1/api/auth/me", nil)
	req.AddCookie(&http.Cookie{Name: "agently_session", Value: sess.ID})
	rec := httptest.NewRecorder()

	ext.handleMe().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if got := strings.TrimSpace(out["displayName"].(string)); got != "Awitas" {
		t.Fatalf("displayName = %q, want %q", got, "Awitas")
	}
}

func TestWithAuthExtensions_CreateSessionThenAuthMe(t *testing.T) {
	runtime := &Runtime{
		cfg: &Config{
			Enabled:    true,
			CookieName: "agently_session",
			OAuth:      &OAuth{Name: "oauth", Mode: "bff"},
		},
		sessions: NewManager(time.Hour, nil),
	}
	runtime.ext = newAuthExtension(runtime.cfg, runtime.sessions, "", nil, nil)

	base := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	handler := WithAuthExtensions(base, runtime)

	exp := time.Now().Add(90 * time.Minute).UTC().Truncate(time.Second)
	claims := map[string]any{
		"sub":                "user-123",
		"email":              "dev@example.com",
		"preferred_username": "devuser",
		"exp":                exp.Unix(),
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	idToken := "x." + base64.RawURLEncoding.EncodeToString(payload) + ".y"

	createReq := httptest.NewRequest(http.MethodPost, "/v1/api/auth/session", strings.NewReader(
		`{"username":"devuser","idToken":"`+idToken+`","accessToken":"token-access"}`,
	))
	createRec := httptest.NewRecorder()
	handler.ServeHTTP(createRec, createReq)

	if createRec.Code != http.StatusOK {
		t.Fatalf("create status = %d, want %d body=%s", createRec.Code, http.StatusOK, createRec.Body.String())
	}
	createResp := createRec.Result()
	defer createResp.Body.Close()
	var sessionCookie *http.Cookie
	for _, c := range createResp.Cookies() {
		if c.Name == "agently_session" {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil || strings.TrimSpace(sessionCookie.Value) == "" {
		t.Fatalf("expected agently_session cookie to be set")
	}

	meReq := httptest.NewRequest(http.MethodGet, "/v1/api/auth/me", nil)
	meReq.AddCookie(&http.Cookie{Name: sessionCookie.Name, Value: sessionCookie.Value})
	meRec := httptest.NewRecorder()
	handler.ServeHTTP(meRec, meReq)

	if meRec.Code != http.StatusOK {
		t.Fatalf("me status = %d, want %d body=%s", meRec.Code, http.StatusOK, meRec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(meRec.Body.Bytes(), &out); err != nil {
		t.Fatalf("json.Unmarshal(me) error = %v", err)
	}
	if got := strings.TrimSpace(out["username"].(string)); got != "devuser" {
		t.Fatalf("username = %q, want %q", got, "devuser")
	}
	if got := strings.TrimSpace(out["provider"].(string)); got != "session" {
		t.Fatalf("provider = %q, want %q", got, "session")
	}
}
