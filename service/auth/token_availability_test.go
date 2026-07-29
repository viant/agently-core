package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	iauth "github.com/viant/agently-core/internal/auth"
	scyauth "github.com/viant/scy/auth"
	"golang.org/x/oauth2"
)

type availabilityUserService struct {
	subjectUser *User
	subjectErr  error
	username    *User
}

func (s *availabilityUserService) GetByUsername(context.Context, string) (*User, error) {
	return s.username, nil
}

func (s *availabilityUserService) GetBySubjectAndProvider(context.Context, string, string) (*User, error) {
	return s.subjectUser, s.subjectErr
}

func (s *availabilityUserService) Upsert(context.Context, *User) error { return nil }

func (s *availabilityUserService) UpsertWithProvider(context.Context, string, string, string, string, string) (string, error) {
	if s.subjectErr != nil {
		return "", s.subjectErr
	}
	if s.subjectUser == nil {
		return "", nil
	}
	return s.subjectUser.ID, nil
}

func (s *availabilityUserService) UpdateHashIPByID(context.Context, string, string) error {
	return nil
}

func (s *availabilityUserService) UpdatePreferences(context.Context, string, *PreferencesPatch) error {
	return nil
}

type availabilityTokenStore struct {
	token       *OAuthToken
	getErr      error
	putErr      error
	deleteCalls int
}

func (s *availabilityTokenStore) Get(context.Context, string, string) (*OAuthToken, error) {
	return s.token, s.getErr
}

func (s *availabilityTokenStore) Put(context.Context, *OAuthToken) error {
	return s.putErr
}

func (s *availabilityTokenStore) Delete(context.Context, string, string) error {
	s.deleteCalls++
	s.token = nil
	return nil
}

func (s *availabilityTokenStore) TryAcquireRefreshLease(context.Context, string, string, string, time.Duration) (int64, bool, error) {
	return 1, true, nil
}

func (s *availabilityTokenStore) ReleaseRefreshLease(context.Context, string, string, string) error {
	return nil
}

func (s *availabilityTokenStore) CASPut(context.Context, *OAuthToken, int64, string) (bool, error) {
	return true, nil
}

type scanningRefreshStore struct {
	*runtimeRefreshTestStore
	scan []*OAuthToken
}

func (s *scanningRefreshStore) ScanExpiring(context.Context, time.Time) ([]*OAuthToken, error) {
	return s.scan, nil
}

func availableStoredToken(userID string) *OAuthToken {
	return &OAuthToken{
		Username:     userID,
		Provider:     "oauth",
		AccessToken:  "stored-access",
		IDToken:      "stored-id",
		RefreshToken: "stored-refresh",
		ExpiresAt:    time.Now().Add(time.Hour),
	}
}

func oauthSessionWithoutTokens(id string) *Session {
	return &Session{
		ID:        id,
		UserID:    "fallback-user",
		Username:  "ppoudyal",
		Subject:   "agently_scheduler",
		Provider:  "oauth",
		CreatedAt: time.Now(),
	}
}

func TestSessionOAuthTokenAvailability_CanonicalAndFallbackMatrix(t *testing.T) {
	tests := []struct {
		name      string
		users     UserService
		store     *availabilityTokenStore
		wantState tokenAvailabilityState
	}{
		{
			name:      "canonical missing is confirmed",
			users:     &availabilityUserService{subjectUser: &User{ID: "canonical-user"}},
			store:     &availabilityTokenStore{},
			wantState: tokenConfirmedMissing,
		},
		{
			name:      "canonical store failure is preserved",
			users:     &availabilityUserService{subjectUser: &User{ID: "canonical-user"}},
			store:     &availabilityTokenStore{getErr: errors.New("db unavailable")},
			wantState: tokenPreserveWithoutInjection,
		},
		{
			name:      "owner lookup failure is preserved",
			users:     &availabilityUserService{subjectErr: errors.New("owner unavailable")},
			store:     &availabilityTokenStore{},
			wantState: tokenPreserveWithoutInjection,
		},
		{
			name:      "fallback missing is preserved",
			users:     &availabilityUserService{},
			store:     &availabilityTokenStore{},
			wantState: tokenPreserveWithoutInjection,
		},
		{
			name:      "fallback can find an available token",
			users:     &availabilityUserService{},
			store:     &availabilityTokenStore{token: availableStoredToken("fallback-user")},
			wantState: tokenAvailable,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sessions := NewManager(time.Hour, nil)
			ext := &authExtension{
				cfg:        &Config{OAuth: &OAuth{Name: "oauth", Mode: "bff"}},
				sessions:   sessions,
				tokenStore: test.store,
				users:      test.users,
			}
			sess := oauthSessionWithoutTokens("availability-session")
			sessions.Put(context.Background(), sess)

			got := ext.sessionOAuthTokenAvailability(context.Background(), sess)
			if got.state != test.wantState {
				t.Fatalf("state = %v, want %v", got.state, test.wantState)
			}
			if got.state == tokenAvailable && got.token == nil {
				t.Fatal("available result has no token")
			}
		})
	}
}

func TestAuthSessionConsumers_ConfirmedMissingAndPreserve(t *testing.T) {
	tests := []struct {
		name        string
		handler     string
		store       *availabilityTokenStore
		wantStatus  int
		wantDeleted bool
		wantCookie  bool
	}{
		{
			name:        "me confirmed missing",
			handler:     "me",
			store:       &availabilityTokenStore{},
			wantStatus:  http.StatusUnauthorized,
			wantDeleted: true,
			wantCookie:  true,
		},
		{
			name:       "me transient store failure",
			handler:    "me",
			store:      &availabilityTokenStore{getErr: errors.New("db unavailable")},
			wantStatus: http.StatusOK,
		},
		{
			name:        "attach confirmed missing",
			handler:     "attach",
			store:       &availabilityTokenStore{},
			wantStatus:  http.StatusUnauthorized,
			wantDeleted: true,
		},
		{
			name:       "attach transient store failure",
			handler:    "attach",
			store:      &availabilityTokenStore{getErr: errors.New("db unavailable")},
			wantStatus: http.StatusServiceUnavailable,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sessions := NewManager(time.Hour, nil)
			ext := &authExtension{
				cfg:        &Config{CookieName: "agently_session", OAuth: &OAuth{Name: "oauth", Mode: "bff"}},
				sessions:   sessions,
				tokenStore: test.store,
				users:      &availabilityUserService{subjectUser: &User{ID: "canonical-user"}},
			}
			sess := oauthSessionWithoutTokens("consumer-session")
			sessions.Put(context.Background(), sess)

			var req *http.Request
			rec := httptest.NewRecorder()
			if test.handler == "me" {
				req = httptest.NewRequest(http.MethodGet, "/v1/api/auth/me", nil)
				req.AddCookie(&http.Cookie{Name: "agently_session", Value: sess.ID})
				ext.handleMe().ServeHTTP(rec, req)
			} else {
				req = httptest.NewRequest(http.MethodPost, "/v1/api/auth/session/attach", strings.NewReader(`{"sessionId":"consumer-session"}`))
				ext.handleAttachSession().ServeHTTP(rec, req)
			}

			if rec.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d body=%s", rec.Code, test.wantStatus, rec.Body.String())
			}
			if deleted := sessions.Get(context.Background(), sess.ID) == nil; deleted != test.wantDeleted {
				t.Fatalf("session deleted = %v, want %v", deleted, test.wantDeleted)
			}
			hasCookie := len(rec.Header().Values("Set-Cookie")) != 0
			if hasCookie != test.wantCookie {
				t.Fatalf("Set-Cookie present = %v, want %v", hasCookie, test.wantCookie)
			}
		})
	}
}

func TestRuntimeRequest_InvalidGrantDeletesRealSession(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	defer server.Close()

	store := &runtimeRefreshTestStore{acquired: true}
	runtime, sess := runtimeRefreshHarness(writeRuntimeRefreshOAuthConfig(t, server.URL), nil, store)
	runtime.cfg.Enabled = true
	runtime.cfg.CookieName = "agently_session"
	runtime.ext.cfg = runtime.cfg

	handler := runtime.protectAll(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("downstream handler called after confirmed invalid_grant")
	}))
	req := httptest.NewRequest(http.MethodGet, "/v1/conversations", nil)
	req.AddCookie(&http.Cookie{Name: runtime.cfg.CookieName, Value: sess.ID})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusUnauthorized, rec.Body.String())
	}
	if got := runtime.sessions.Get(context.Background(), sess.ID); got != nil {
		t.Fatalf("session survived request invalid_grant: %#v", got)
	}
	if store.deleteCalls.Load() != 1 {
		t.Fatalf("token delete calls = %d, want 1", store.deleteCalls.Load())
	}
}

func TestActiveSessionWatcher_InvalidGrantDefersSessionDeleteToNextRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	defer server.Close()

	store := &runtimeRefreshTestStore{acquired: true}
	runtime, sess := runtimeRefreshHarness(writeRuntimeRefreshOAuthConfig(t, server.URL), nil, store)
	runtime.cfg.Enabled = true
	runtime.cfg.CookieName = "agently_session"
	runtime.ext.cfg = runtime.cfg
	runtime.ext.users = &testUserService{userBySubjectProvider: map[string]*User{
		sess.Subject + "|oauth": {ID: "user-42", Username: sess.Username},
	}}
	store.token = &OAuthToken{
		Username:     "user-42",
		Provider:     "oauth",
		AccessToken:  sess.Tokens.AccessToken,
		IDToken:      sess.Tokens.IDToken,
		RefreshToken: sess.Tokens.RefreshToken,
		ExpiresAt:    sess.Tokens.Expiry,
	}

	runtime.refreshExpiringSessions(context.Background())

	afterWatcher := runtime.sessions.Get(context.Background(), sess.ID)
	if afterWatcher == nil {
		t.Fatal("watcher physically deleted the real session")
	}
	if afterWatcher.Tokens != nil {
		t.Fatalf("watcher did not preserve baseline token invalidation: %#v", afterWatcher.Tokens)
	}
	if store.deleteCalls.Load() != 1 {
		t.Fatalf("watcher token delete calls = %d, want 1", store.deleteCalls.Load())
	}

	handler := runtime.protectAll(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("downstream handler called for watcher-invalidated session")
	}))
	req := httptest.NewRequest(http.MethodGet, "/v1/conversations", nil)
	req.AddCookie(&http.Cookie{Name: runtime.cfg.CookieName, Value: sess.ID})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("next request status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	if got := runtime.sessions.Get(context.Background(), sess.ID); got != nil {
		t.Fatalf("next request did not delete confirmed-missing session: %#v", got)
	}
}

func TestTokenStoreWatcher_InvalidGrantDoesNotDeleteRealBrowserSession(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	defer server.Close()

	expired := &OAuthToken{
		Username:     "store-user",
		Provider:     "oauth",
		AccessToken:  "store-access",
		IDToken:      "store-id",
		RefreshToken: "store-refresh",
		ExpiresAt:    time.Now().Add(-time.Minute),
	}
	baseStore := &runtimeRefreshTestStore{acquired: true}
	baseStore.token = expired
	store := &scanningRefreshStore{runtimeRefreshTestStore: baseStore, scan: []*OAuthToken{expired}}
	cfg := &Config{
		Enabled:    true,
		CookieName: "agently_session",
		OAuth: &OAuth{Name: "oauth", Mode: "bff", Client: &OAuthClient{
			ConfigURL: writeRuntimeRefreshOAuthConfig(t, server.URL),
		}},
	}
	sessions := NewManager(time.Hour, nil)
	runtime := &Runtime{
		cfg:      cfg,
		sessions: sessions,
		ext:      &authExtension{cfg: cfg, sessions: sessions, tokenStore: store},
	}
	browserTokens := &scyauth.Token{
		Token:   oauth2.Token{AccessToken: "browser-access", RefreshToken: "browser-refresh", Expiry: time.Now().Add(time.Hour)},
		IDToken: "browser-id",
	}
	sessions.Put(context.Background(), &Session{
		ID:       "browser-session",
		Username: "browser-user",
		Subject:  "browser-subject",
		Provider: "oauth",
		Tokens:   browserTokens,
	})

	runtime.refreshTokenStore(context.Background())

	browser := sessions.Get(context.Background(), "browser-session")
	if browser == nil || browser.Tokens != browserTokens {
		t.Fatalf("token-store watcher changed real browser session: %#v", browser)
	}
	if store.deleteCalls.Load() != 1 {
		t.Fatalf("token-store watcher delete calls = %d, want 1", store.deleteCalls.Load())
	}
	synthetic := sessions.Get(context.Background(), "store-refresh-store-user")
	if synthetic == nil || synthetic.Tokens != nil {
		t.Fatalf("baseline synthetic store-refresh artifact missing or unexpected: %#v", synthetic)
	}
}

func TestRuntimeRefreshCooldown_ValidTokenIsStillInjected(t *testing.T) {
	cfg := &Config{Enabled: true, CookieName: "agently_session", OAuth: &OAuth{Name: "oauth", Mode: "bff"}}
	sessions := NewManager(time.Hour, nil)
	runtime := &Runtime{cfg: cfg, sessions: sessions, ext: &authExtension{cfg: cfg, sessions: sessions}}
	sess := &Session{
		ID:                      "valid-cooldown",
		Username:                "ppoudyal",
		Subject:                 "agently_scheduler",
		Provider:                "oauth",
		TransientRefreshRetryAt: time.Now().Add(time.Minute),
		Tokens: &scyauth.Token{
			Token: oauth2.Token{
				AccessToken:  "valid-access",
				RefreshToken: "refresh",
				Expiry:       time.Now().Add(time.Hour),
			},
			IDToken: "valid-id",
		},
	}
	sessions.Put(context.Background(), sess)

	var downstream *scyauth.Token
	handler := runtime.protectAll(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		downstream = iauth.TokensFromContext(req.Context())
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/v1/conversations", nil)
	req.AddCookie(&http.Cookie{Name: cfg.CookieName, Value: sess.ID})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if downstream == nil || downstream.AccessToken != "valid-access" {
		t.Fatalf("valid token was withheld during cooldown: %#v", downstream)
	}
}

func TestCreateSession_PersistenceFailureDoesNotPublishSessionOrCookie(t *testing.T) {
	sessions := NewManager(time.Hour, nil)
	ext := &authExtension{
		cfg:        &Config{CookieName: "agently_session", OAuth: &OAuth{Name: "oauth", Mode: "bff"}},
		sessions:   sessions,
		tokenStore: &availabilityTokenStore{},
		users:      &availabilityUserService{subjectErr: errors.New("owner unavailable")},
	}
	token := fakeJWTWithClaims(t, map[string]any{
		"sub":                "agently_scheduler",
		"preferred_username": "ppoudyal",
		"exp":                time.Now().Add(time.Hour).Unix(),
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/api/auth/session", strings.NewReader(
		`{"username":"ppoudyal","idToken":"`+token+`","accessToken":"access","refreshToken":"refresh"}`,
	))
	rec := httptest.NewRecorder()
	ext.handleCreateSession().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}
	if active := sessions.ActiveSessions(); len(active) != 0 {
		t.Fatalf("session published after persistence failure: %#v", active)
	}
	if cookies := rec.Header().Values("Set-Cookie"); len(cookies) != 0 {
		t.Fatalf("cookie published after persistence failure: %#v", cookies)
	}
}
