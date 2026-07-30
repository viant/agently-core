package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	iauth "github.com/viant/agently-core/internal/auth"
	scyauth "github.com/viant/scy/auth"
	"golang.org/x/oauth2"
)

type lifecycleUserService struct {
	subjectUser  *User
	subjectErr   error
	usernameUser *User
	usernameErr  error
}

func (s *lifecycleUserService) GetByUsername(context.Context, string) (*User, error) {
	return s.usernameUser, s.usernameErr
}

func (s *lifecycleUserService) GetBySubjectAndProvider(context.Context, string, string) (*User, error) {
	return s.subjectUser, s.subjectErr
}

func (s *lifecycleUserService) Upsert(context.Context, *User) error { return nil }

func (s *lifecycleUserService) UpsertWithProvider(context.Context, string, string, string, string, string) (string, error) {
	if s.subjectErr != nil {
		return "", s.subjectErr
	}
	if s.subjectUser == nil {
		return "", nil
	}
	return s.subjectUser.ID, nil
}

func (s *lifecycleUserService) UpdateHashIPByID(context.Context, string, string) error {
	return nil
}

func (s *lifecycleUserService) UpdatePreferences(context.Context, string, *PreferencesPatch) error {
	return nil
}

type lifecycleTokenStore struct {
	mu           sync.Mutex
	token        *OAuthToken
	getErr       error
	putErr       error
	acquireErr   error
	acquired     bool
	scan         []*OAuthToken
	getCalls     int
	putCalls     int
	deleteCalls  int
	acquireCalls int
	lastGetOwner string
}

func (s *lifecycleTokenStore) Get(_ context.Context, username, _ string) (*OAuthToken, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.getCalls++
	s.lastGetOwner = username
	return s.token, s.getErr
}

func (s *lifecycleTokenStore) Put(_ context.Context, token *OAuthToken) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.putCalls++
	if s.putErr == nil {
		s.token = token
	}
	return s.putErr
}

func (s *lifecycleTokenStore) Delete(context.Context, string, string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleteCalls++
	s.token = nil
	return nil
}

func (s *lifecycleTokenStore) TryAcquireRefreshLease(context.Context, string, string, string, time.Duration) (int64, bool, error) {
	s.mu.Lock()
	s.acquireCalls++
	s.mu.Unlock()
	return 1, s.acquired, s.acquireErr
}

func (s *lifecycleTokenStore) ReleaseRefreshLease(context.Context, string, string, string) error {
	return nil
}

func (s *lifecycleTokenStore) CASPut(context.Context, *OAuthToken, int64, string) (bool, error) {
	return false, nil
}

func (s *lifecycleTokenStore) ScanExpiring(context.Context, time.Time) ([]*OAuthToken, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*OAuthToken(nil), s.scan...), nil
}

func (s *lifecycleTokenStore) counts() (gets, puts, deletes int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getCalls, s.putCalls, s.deleteCalls
}

func (s *lifecycleTokenStore) leaseAcquires() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.acquireCalls
}

type lifecycleSessionStore struct {
	mu          sync.Mutex
	upsertCalls int
	deleteCalls int
}

func (s *lifecycleSessionStore) Get(context.Context, string) (*SessionRecord, error) {
	return nil, nil
}

func (s *lifecycleSessionStore) Upsert(context.Context, *SessionRecord) error {
	s.mu.Lock()
	s.upsertCalls++
	s.mu.Unlock()
	return nil
}

func (s *lifecycleSessionStore) Delete(context.Context, string) error {
	s.mu.Lock()
	s.deleteCalls++
	s.mu.Unlock()
	return nil
}

func (s *lifecycleSessionStore) deletes() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deleteCalls
}

func lifecycleConfig() *Config {
	return &Config{
		Enabled:    true,
		CookieName: "agently_session",
		OAuth: &OAuth{
			Name:   "oauth",
			Mode:   "bff",
			Client: &OAuthClient{},
		},
	}
}

func lifecycleSession(id string, token *scyauth.Token) *Session {
	return &Session{
		ID:        id,
		UserID:    "user-42",
		Username:  "display-user",
		Subject:   "provider-subject",
		Provider:  "oauth",
		Tokens:    token,
		CreatedAt: time.Now(),
	}
}

func lifecycleValidToken(access string) *scyauth.Token {
	return &scyauth.Token{
		Token: oauth2.Token{
			AccessToken:  access,
			RefreshToken: "refresh",
			Expiry:       time.Now().Add(time.Hour),
		},
	}
}

func lifecycleStoredToken(access string, expiry time.Time) *OAuthToken {
	return &OAuthToken{
		Username:     "user-42",
		Provider:     "oauth",
		AccessToken:  access,
		RefreshToken: "refresh",
		ExpiresAt:    expiry,
	}
}

func TestResolveSessionOAuthToken_EvidenceMatrix(t *testing.T) {
	canonical := &User{ID: "user-42", Username: "display-user", Subject: "provider-subject", Provider: "oauth"}
	tests := []struct {
		name      string
		users     UserService
		store     *lifecycleTokenStore
		session   *Session
		wantState tokenAvailabilityState
		wantOwner string
		wantGets  int
	}{
		{
			name:      "valid scoped session wins",
			users:     &lifecycleUserService{subjectUser: canonical},
			store:     &lifecycleTokenStore{getErr: errors.New("must not read store")},
			session:   lifecycleSession("session-current", lifecycleValidToken("session-access")),
			wantState: tokenAvailable,
			wantGets:  0,
		},
		{
			name:      "canonical hit is available",
			users:     &lifecycleUserService{subjectUser: canonical},
			store:     &lifecycleTokenStore{token: lifecycleStoredToken("stored-access", time.Now().Add(time.Hour))},
			session:   lifecycleSession("session-canonical-hit", nil),
			wantState: tokenAvailable,
			wantOwner: "user-42",
			wantGets:  1,
		},
		{
			name:  "canonical hit from another provider preserves",
			users: &lifecycleUserService{subjectUser: canonical},
			store: &lifecycleTokenStore{token: &OAuthToken{
				Username:    "user-42",
				Provider:    "other",
				AccessToken: "other-provider-access",
				ExpiresAt:   time.Now().Add(time.Hour),
			}},
			session:   lifecycleSession("session-provider-mismatch", nil),
			wantState: tokenPreserveWithoutInjection,
			wantOwner: "user-42",
			wantGets:  1,
		},
		{
			name:      "canonical miss is confirmed",
			users:     &lifecycleUserService{subjectUser: canonical},
			store:     &lifecycleTokenStore{},
			session:   lifecycleSession("session-canonical-miss", nil),
			wantState: tokenConfirmedMissing,
			wantOwner: "user-42",
			wantGets:  1,
		},
		{
			name:      "canonical store error preserves",
			users:     &lifecycleUserService{subjectUser: canonical},
			store:     &lifecycleTokenStore{getErr: errors.New("store unavailable")},
			session:   lifecycleSession("session-canonical-error", nil),
			wantState: tokenPreserveWithoutInjection,
			wantOwner: "user-42",
			wantGets:  1,
		},
		{
			name:      "fallback hit is available",
			users:     &lifecycleUserService{},
			store:     &lifecycleTokenStore{token: lifecycleStoredToken("fallback-access", time.Now().Add(time.Hour))},
			session:   &Session{ID: "session-fallback-hit", Username: "display-user", Subject: "provider-subject", Provider: "oauth"},
			wantState: tokenAvailable,
			wantOwner: "provider-subject",
			wantGets:  1,
		},
		{
			name:      "fallback miss preserves",
			users:     &lifecycleUserService{},
			store:     &lifecycleTokenStore{},
			session:   &Session{ID: "session-fallback-miss", Username: "display-user", Subject: "provider-subject", Provider: "oauth"},
			wantState: tokenPreserveWithoutInjection,
			wantOwner: "provider-subject",
			wantGets:  1,
		},
		{
			name:      "lookup error fallback hit preserves",
			users:     &lifecycleUserService{subjectErr: errors.New("lookup unavailable")},
			store:     &lifecycleTokenStore{token: lifecycleStoredToken("fallback-access", time.Now().Add(time.Hour))},
			session:   lifecycleSession("session-lookup-error-hit", nil),
			wantState: tokenPreserveWithoutInjection,
			wantGets:  0,
		},
		{
			name:      "lookup error fallback miss preserves",
			users:     &lifecycleUserService{subjectErr: errors.New("lookup unavailable")},
			store:     &lifecycleTokenStore{},
			session:   lifecycleSession("session-lookup-error-miss", nil),
			wantState: tokenPreserveWithoutInjection,
			wantGets:  0,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := resolveSessionOAuthToken(context.Background(), test.users, test.store, "oauth", &OAuthClient{}, test.session)
			if result.state != test.wantState {
				t.Fatalf("state = %v, want %v", result.state, test.wantState)
			}
			gets, _, _ := test.store.counts()
			if gets != test.wantGets {
				t.Fatalf("store.Get calls = %d, want %d", gets, test.wantGets)
			}
			if test.wantOwner != "" && test.store.lastGetOwner != test.wantOwner {
				t.Fatalf("store owner = %q, want %q", test.store.lastGetOwner, test.wantOwner)
			}
			if result.state == tokenAvailable && result.token == nil {
				t.Fatal("available result has no token")
			}
		})
	}
}

func TestRuntimeTryLoadFreshTokenFromStoreRejectsMismatchedAndRefreshOnlyTokens(t *testing.T) {
	canonical := &lifecycleUserService{subjectUser: &User{ID: "user-42", Subject: "provider-subject", Provider: "oauth"}}
	tests := []struct {
		name  string
		token *OAuthToken
	}{
		{
			name: "mismatched provider",
			token: &OAuthToken{
				Username:    "user-42",
				Provider:    "other",
				AccessToken: "other-provider-access",
				ExpiresAt:   time.Now().Add(time.Hour),
			},
		},
		{
			name: "refresh only",
			token: &OAuthToken{
				Username:     "user-42",
				Provider:     "oauth",
				RefreshToken: "stored-refresh",
				ExpiresAt:    time.Now().Add(time.Hour),
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := lifecycleConfig()
			sessions := NewManager(time.Hour, nil)
			store := &lifecycleTokenStore{token: test.token}
			runtime := &Runtime{
				cfg:      cfg,
				sessions: sessions,
				ext:      newAuthExtension(cfg, sessions, "", store, canonical),
			}
			sess := lifecycleSession("load-"+test.name, nil)

			result := runtime.tryLoadFreshTokenFromStore(context.Background(), sess)

			if result.state != tokenPreserveWithoutInjection {
				t.Fatalf("state = %v, want preserve without injection", result.state)
			}
			if result.token != nil {
				t.Fatalf("result token = %#v, want nil", result.token)
			}
			if sess.Tokens != nil {
				t.Fatalf("session token was injected: %#v", sess.Tokens)
			}
			gets, _, _ := store.counts()
			if gets != 1 {
				t.Fatalf("store.Get calls = %d, want 1", gets)
			}
		})
	}
}

func TestRuntimeOwnerLookupErrorPreservesExpiredSessionWithoutStoreOrRefresh(t *testing.T) {
	var idpCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		idpCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"unexpected","expires_in":3600}`))
	}))
	defer server.Close()

	cfg := lifecycleConfig()
	cfg.OAuth.Client.ConfigURL = writeRuntimeRefreshOAuthConfig(t, server.URL)
	sessions := NewManager(time.Hour, nil)
	expiredAt := time.Now().Add(-time.Minute)
	sess := lifecycleSession("owner-lookup-error", &scyauth.Token{
		Token: oauth2.Token{
			AccessToken:  "expired-access",
			RefreshToken: "expired-refresh",
			Expiry:       expiredAt,
		},
		IDToken: "expired-id",
	})
	sessions.Put(context.Background(), sess)
	store := &lifecycleTokenStore{
		token:    lifecycleStoredToken("stored-access", time.Now().Add(time.Hour)),
		acquired: true,
	}
	runtime := &Runtime{
		cfg:      cfg,
		sessions: sessions,
		ext: newAuthExtension(cfg, sessions, "", store, &lifecycleUserService{
			subjectErr: errors.New("owner lookup unavailable"),
		}),
	}
	var (
		downstreamCalled bool
		downstreamToken  *scyauth.Token
		downstreamUser   string
	)
	handler := runtime.protectAll(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		downstreamCalled = true
		downstreamToken = iauth.TokensFromContext(req.Context())
		downstreamUser = EffectiveUserID(req.Context())
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/v1/query", nil)
	req.AddCookie(&http.Cookie{Name: cfg.CookieName, Value: sess.ID})
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || !downstreamCalled {
		t.Fatalf("status = %d, downstream called = %v, body=%s", rec.Code, downstreamCalled, rec.Body.String())
	}
	if downstreamUser != "provider-subject" {
		t.Fatalf("effective user = %q, want provider subject", downstreamUser)
	}
	if downstreamToken != nil {
		t.Fatalf("downstream token = %#v, want no injection", downstreamToken)
	}
	if got := sessions.Get(context.Background(), sess.ID); got == nil || got.Tokens == nil || got.Tokens.RefreshToken != "expired-refresh" {
		t.Fatalf("session was not preserved: %#v", got)
	}
	gets, puts, deletes := store.counts()
	if gets != 0 || puts != 0 || deletes != 0 {
		t.Fatalf("token store calls = get:%d put:%d delete:%d, want all zero", gets, puts, deletes)
	}
	if acquired := store.leaseAcquires(); acquired != 0 {
		t.Fatalf("refresh lease calls = %d, want 0", acquired)
	}
	if got := idpCalls.Load(); got != 0 {
		t.Fatalf("IdP calls = %d, want 0", got)
	}
}

func TestRuntimeTokenAvailabilityMatrix(t *testing.T) {
	canonical := &User{ID: "user-42", Subject: "provider-subject", Provider: "oauth"}
	tests := []struct {
		name       string
		users      UserService
		tokenStore *lifecycleTokenStore
		token      *scyauth.Token
		wantStatus int
		wantToken  bool
		wantDelete int
	}{
		{
			name:       "available",
			tokenStore: &lifecycleTokenStore{},
			token:      lifecycleValidToken("session-access"),
			wantStatus: http.StatusOK,
			wantToken:  true,
		},
		{
			name:       "confirmed missing",
			users:      &lifecycleUserService{subjectUser: canonical},
			tokenStore: &lifecycleTokenStore{},
			wantStatus: http.StatusUnauthorized,
			wantDelete: 1,
		},
		{
			name:       "preserve without injection",
			users:      &lifecycleUserService{subjectUser: canonical},
			tokenStore: &lifecycleTokenStore{getErr: errors.New("store unavailable")},
			wantStatus: http.StatusOK,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := lifecycleConfig()
			sessionStore := &lifecycleSessionStore{}
			sessions := NewManager(time.Hour, sessionStore)
			sess := lifecycleSession("runtime-"+test.name, test.token)
			sessions.Put(context.Background(), sess)
			runtime := &Runtime{
				cfg:      cfg,
				sessions: sessions,
				ext:      newAuthExtension(cfg, sessions, "", test.tokenStore, test.users),
			}
			var (
				downstreamCalled bool
				downstreamToken  *scyauth.Token
				downstreamUser   string
			)
			handler := runtime.protectAll(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				downstreamCalled = true
				downstreamToken = iauth.TokensFromContext(req.Context())
				downstreamUser = EffectiveUserID(req.Context())
				w.WriteHeader(http.StatusOK)
			}))
			req := httptest.NewRequest(http.MethodGet, "/v1/query", nil)
			req.AddCookie(&http.Cookie{Name: cfg.CookieName, Value: sess.ID})
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d body=%s", rec.Code, test.wantStatus, rec.Body.String())
			}
			if downstreamCalled != (test.wantStatus == http.StatusOK) {
				t.Fatalf("downstream called = %v", downstreamCalled)
			}
			if (downstreamToken != nil) != test.wantToken {
				t.Fatalf("downstream token = %#v, want present=%v", downstreamToken, test.wantToken)
			}
			if downstreamCalled && downstreamUser != "provider-subject" {
				t.Fatalf("effective user = %q, want provider subject", downstreamUser)
			}
			if sessionStore.deletes() != test.wantDelete {
				t.Fatalf("Session.Delete calls = %d, want %d", sessionStore.deletes(), test.wantDelete)
			}
			if test.wantDelete == 0 && sessions.Get(context.Background(), sess.ID) == nil {
				t.Fatal("session was not preserved")
			}
		})
	}
}

func TestRuntimeHandleMeTokenAvailabilityMatrix(t *testing.T) {
	canonical := &User{ID: "user-42", Subject: "provider-subject", Provider: "oauth"}
	tests := []struct {
		name            string
		users           UserService
		tokenStore      *lifecycleTokenStore
		token           *scyauth.Token
		wantStatus      int
		wantDelete      int
		wantClearCookie bool
	}{
		{
			name:       "available",
			tokenStore: &lifecycleTokenStore{},
			token:      lifecycleValidToken("session-access"),
			wantStatus: http.StatusOK,
		},
		{
			name:            "confirmed missing",
			users:           &lifecycleUserService{subjectUser: canonical},
			tokenStore:      &lifecycleTokenStore{},
			wantStatus:      http.StatusUnauthorized,
			wantDelete:      1,
			wantClearCookie: true,
		},
		{
			name:       "preserve without injection",
			users:      &lifecycleUserService{subjectUser: canonical},
			tokenStore: &lifecycleTokenStore{getErr: errors.New("store unavailable")},
			wantStatus: http.StatusOK,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := lifecycleConfig()
			sessionStore := &lifecycleSessionStore{}
			sessions := NewManager(time.Hour, sessionStore)
			sess := lifecycleSession("me-"+test.name, test.token)
			sessions.Put(context.Background(), sess)
			runtime := &Runtime{
				cfg:      cfg,
				sessions: sessions,
				ext:      newAuthExtension(cfg, sessions, "", test.tokenStore, test.users),
			}
			handler := WithAuthExtensions(http.NotFoundHandler(), runtime)
			req := httptest.NewRequest(http.MethodGet, "/v1/api/auth/me", nil)
			req.AddCookie(&http.Cookie{Name: cfg.CookieName, Value: sess.ID})
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d body=%s", rec.Code, test.wantStatus, rec.Body.String())
			}
			if sessionStore.deletes() != test.wantDelete {
				t.Fatalf("Session.Delete calls = %d, want %d", sessionStore.deletes(), test.wantDelete)
			}
			cleared := false
			for _, cookie := range rec.Result().Cookies() {
				if cookie.Name == cfg.CookieName && cookie.MaxAge < 0 {
					cleared = true
				}
			}
			if cleared != test.wantClearCookie {
				t.Fatalf("clear cookie = %v, want %v", cleared, test.wantClearCookie)
			}
			if test.wantDelete == 0 && sessions.Get(context.Background(), sess.ID) == nil {
				t.Fatal("session was not preserved")
			}
		})
	}
}

func TestRuntimeHandleAttachTokenAvailabilityMatrix(t *testing.T) {
	canonical := &User{ID: "user-42", Subject: "provider-subject", Provider: "oauth"}
	tests := []struct {
		name       string
		users      UserService
		tokenStore *lifecycleTokenStore
		token      *scyauth.Token
		wantStatus int
		wantCookie bool
		wantDelete int
	}{
		{
			name:       "available",
			tokenStore: &lifecycleTokenStore{},
			token:      lifecycleValidToken("session-access"),
			wantStatus: http.StatusOK,
			wantCookie: true,
		},
		{
			name:       "confirmed missing",
			users:      &lifecycleUserService{subjectUser: canonical},
			tokenStore: &lifecycleTokenStore{},
			wantStatus: http.StatusUnauthorized,
			wantDelete: 1,
		},
		{
			name:       "preserve without injection",
			users:      &lifecycleUserService{subjectUser: canonical},
			tokenStore: &lifecycleTokenStore{getErr: errors.New("store unavailable")},
			wantStatus: http.StatusServiceUnavailable,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := lifecycleConfig()
			sessionStore := &lifecycleSessionStore{}
			sessions := NewManager(time.Hour, sessionStore)
			sess := lifecycleSession("attach-"+test.name, test.token)
			sessions.Put(context.Background(), sess)
			runtime := &Runtime{
				cfg:      cfg,
				sessions: sessions,
				ext:      newAuthExtension(cfg, sessions, "", test.tokenStore, test.users),
			}
			handler := WithAuthExtensions(http.NotFoundHandler(), runtime)
			req := httptest.NewRequest(http.MethodPost, "/v1/api/auth/session/attach",
				strings.NewReader(`{"sessionId":"`+sess.ID+`"}`))
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d body=%s", rec.Code, test.wantStatus, rec.Body.String())
			}
			hasCookie := false
			for _, cookie := range rec.Result().Cookies() {
				if cookie.Name == cfg.CookieName && cookie.Value == sess.ID && cookie.MaxAge >= 0 {
					hasCookie = true
				}
			}
			if hasCookie != test.wantCookie {
				t.Fatalf("session cookie = %v, want %v", hasCookie, test.wantCookie)
			}
			if sessionStore.deletes() != test.wantDelete {
				t.Fatalf("attach Session.Delete calls = %d, want %d", sessionStore.deletes(), test.wantDelete)
			}
			if present := sessions.Get(context.Background(), sess.ID) != nil; present != (test.wantDelete == 0) {
				t.Fatalf("session present = %v, want %v", present, test.wantDelete == 0)
			}
		})
	}
}

func TestRuntimeRequestInvalidGrantDeletesRealSession(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	defer server.Close()

	cfg := lifecycleConfig()
	cfg.OAuth.Client.ConfigURL = writeRuntimeRefreshOAuthConfig(t, server.URL)
	sessionStore := &lifecycleSessionStore{}
	sessions := NewManager(time.Hour, sessionStore)
	expired := time.Now().Add(-time.Minute)
	tokenStore := &lifecycleTokenStore{
		token:    lifecycleStoredToken("old-access", expired),
		acquired: true,
	}
	sess := lifecycleSession("request-invalid-grant", &scyauth.Token{
		Token: oauth2.Token{AccessToken: "old-access", RefreshToken: "refresh", Expiry: expired},
	})
	sessions.Put(context.Background(), sess)
	runtime := &Runtime{
		cfg:      cfg,
		sessions: sessions,
		ext:      newAuthExtension(cfg, sessions, "", tokenStore, nil),
	}
	handler := runtime.protectAll(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("downstream called after invalid_grant")
	}))
	req := httptest.NewRequest(http.MethodGet, "/v1/query", nil)
	req.AddCookie(&http.Cookie{Name: cfg.CookieName, Value: sess.ID})
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusUnauthorized, rec.Body.String())
	}
	if sessions.Get(context.Background(), sess.ID) != nil {
		t.Fatal("real session was not deleted")
	}
	if sessionStore.deletes() != 1 {
		t.Fatalf("Session.Delete calls = %d, want 1", sessionStore.deletes())
	}
	_, _, tokenDeletes := tokenStore.counts()
	if tokenDeletes != 1 {
		t.Fatalf("token Delete calls = %d, want 1", tokenDeletes)
	}
}

func TestRuntimeWatcherInvalidGrantDefersSessionDeleteUntilNextRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	defer server.Close()

	cfg := lifecycleConfig()
	cfg.OAuth.Client.ConfigURL = writeRuntimeRefreshOAuthConfig(t, server.URL)
	sessionStore := &lifecycleSessionStore{}
	sessions := NewManager(time.Hour, sessionStore)
	expired := time.Now().Add(-time.Minute)
	tokenStore := &lifecycleTokenStore{
		token:    lifecycleStoredToken("old-access", expired),
		acquired: true,
	}
	users := &lifecycleUserService{subjectUser: &User{ID: "user-42", Subject: "provider-subject", Provider: "oauth"}}
	sess := lifecycleSession("watcher-invalid-grant", &scyauth.Token{
		Token: oauth2.Token{AccessToken: "old-access", RefreshToken: "refresh", Expiry: expired},
	})
	sessions.Put(context.Background(), sess)
	runtime := &Runtime{
		cfg:      cfg,
		sessions: sessions,
		ext:      newAuthExtension(cfg, sessions, "", tokenStore, users),
	}

	runtime.refreshExpiringSessions(context.Background())

	if sessionStore.deletes() != 0 {
		t.Fatalf("watcher Session.Delete calls = %d, want 0", sessionStore.deletes())
	}
	preserved := sessions.Get(context.Background(), sess.ID)
	if preserved == nil {
		t.Fatal("watcher removed active session")
	}
	if preserved.Tokens != nil {
		t.Fatalf("watcher tokens = %#v, want cleared", preserved.Tokens)
	}
	_, _, tokenDeletes := tokenStore.counts()
	if tokenDeletes != 1 {
		t.Fatalf("watcher token Delete calls = %d, want 1", tokenDeletes)
	}

	handler := runtime.protectAll(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("downstream called after confirmed missing token")
	}))
	req := httptest.NewRequest(http.MethodGet, "/v1/query", nil)
	req.AddCookie(&http.Cookie{Name: cfg.CookieName, Value: sess.ID})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("next request status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	if sessionStore.deletes() != 1 {
		t.Fatalf("next request Session.Delete calls = %d, want 1", sessionStore.deletes())
	}
}

func TestRuntimeStoreRefreshInvalidGrantDoesNotDeleteRealSession(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	defer server.Close()

	cfg := lifecycleConfig()
	cfg.OAuth.Client.ConfigURL = writeRuntimeRefreshOAuthConfig(t, server.URL)
	sessionStore := &lifecycleSessionStore{}
	sessions := NewManager(time.Hour, sessionStore)
	expired := lifecycleStoredToken("old-access", time.Now().Add(-time.Minute))
	tokenStore := &lifecycleTokenStore{
		token:    expired,
		scan:     []*OAuthToken{expired},
		acquired: true,
	}
	real := lifecycleSession("real-session", lifecycleValidToken("real-access"))
	sessions.Put(context.Background(), real)
	runtime := &Runtime{
		cfg:      cfg,
		sessions: sessions,
		ext:      newAuthExtension(cfg, sessions, "", tokenStore, nil),
	}

	runtime.refreshTokenStore(context.Background())

	if sessionStore.deletes() != 0 {
		t.Fatalf("store refresh Session.Delete calls = %d, want 0", sessionStore.deletes())
	}
	if got := sessions.Get(context.Background(), real.ID); got == nil || got.Tokens == nil || got.Tokens.AccessToken != "real-access" {
		t.Fatalf("real session changed by store refresh: %#v", got)
	}
	synthetic := sessions.Get(context.Background(), "store-refresh-user-42")
	if synthetic == nil || synthetic.Tokens != nil {
		t.Fatalf("store-refresh artifact = %#v, want preserved synthetic session with cleared tokens", synthetic)
	}
}

func TestRuntimeCooldownStillInjectsValidScopedSessionToken(t *testing.T) {
	scoped := fakeJWTWithClaims(t, map[string]any{"scope": "openid role-a"})
	cfg := lifecycleConfig()
	cfg.OAuth.Client.Scopes = []string{"openid", "role-a"}
	sessions := NewManager(time.Hour, nil)
	sess := lifecycleSession("cooldown-valid", &scyauth.Token{
		Token: oauth2.Token{
			AccessToken:  scoped,
			RefreshToken: "refresh",
			Expiry:       time.Now().Add(time.Hour),
		},
	})
	sess.Scopes = []string{"openid", "role-a"}
	sess.TransientRefreshRetryAt = time.Now().Add(time.Minute)
	sessions.Put(context.Background(), sess)
	runtime := &Runtime{cfg: cfg, sessions: sessions}
	var downstreamToken *scyauth.Token
	handler := runtime.protectAll(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		downstreamToken = iauth.TokensFromContext(req.Context())
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/v1/query", nil)
	req.AddCookie(&http.Cookie{Name: cfg.CookieName, Value: sess.ID})
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if downstreamToken == nil || downstreamToken.AccessToken != scoped {
		t.Fatalf("valid token was suppressed by cooldown: %#v", downstreamToken)
	}
}
