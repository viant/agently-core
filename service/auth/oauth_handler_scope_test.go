package auth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/viant/scy/auth/authorizer"
	"golang.org/x/oauth2"
)

type staticOOBAuthorizer struct {
	token *oauth2.Token
}

func (s *staticOOBAuthorizer) Authorize(context.Context, *authorizer.Command) (*oauth2.Token, error) {
	return s.token, nil
}

type scopeRejectTokenStore struct {
	putCalled chan struct{}
	putErr    error
	beforePut func()
}

func newScopeRejectTokenStore() *scopeRejectTokenStore {
	return &scopeRejectTokenStore{putCalled: make(chan struct{}, 1)}
}

func (s *scopeRejectTokenStore) Get(context.Context, string, string) (*OAuthToken, error) {
	return nil, nil
}

func (s *scopeRejectTokenStore) Put(context.Context, *OAuthToken) error {
	if s.beforePut != nil {
		s.beforePut()
	}
	select {
	case s.putCalled <- struct{}{}:
	default:
	}
	return s.putErr
}

func (s *scopeRejectTokenStore) Delete(context.Context, string, string) error {
	return nil
}

func (s *scopeRejectTokenStore) TryAcquireRefreshLease(context.Context, string, string, string, time.Duration) (int64, bool, error) {
	return 0, false, nil
}

func (s *scopeRejectTokenStore) ReleaseRefreshLease(context.Context, string, string, string) error {
	return nil
}

func (s *scopeRejectTokenStore) CASPut(context.Context, *OAuthToken, int64, string) (bool, error) {
	return false, nil
}

func assertNoRejectedTokenPersistence(t *testing.T, sessions *Manager, store *scopeRejectTokenStore, rec *httptest.ResponseRecorder) {
	t.Helper()
	assertNoSessionExposure(t, sessions, rec)
	select {
	case <-store.putCalled:
		t.Fatal("rejected token was persisted")
	case <-time.After(50 * time.Millisecond):
	}
}

func assertNoSessionExposure(t *testing.T, sessions *Manager, rec *httptest.ResponseRecorder) {
	t.Helper()
	if active := sessions.ActiveSessions(); len(active) != 0 {
		t.Fatalf("rejected token created sessions: %#v", active)
	}
	if cookies := rec.Result().Cookies(); len(cookies) != 0 {
		t.Fatalf("rejected token wrote session cookies: %#v", cookies)
	}
}

type failingOAuthPersistUserService struct {
	err error
}

func (f *failingOAuthPersistUserService) GetByUsername(context.Context, string) (*User, error) {
	return nil, nil
}

func (f *failingOAuthPersistUserService) GetBySubjectAndProvider(context.Context, string, string) (*User, error) {
	return nil, nil
}

func (f *failingOAuthPersistUserService) Upsert(context.Context, *User) error {
	return nil
}

func (f *failingOAuthPersistUserService) UpsertWithProvider(context.Context, string, string, string, string, string) (string, error) {
	return "", f.err
}

func (f *failingOAuthPersistUserService) UpdateHashIPByID(context.Context, string, string) error {
	return nil
}

func (f *failingOAuthPersistUserService) UpdatePreferences(context.Context, string, *PreferencesPatch) error {
	return nil
}

func TestAuthExtensionOAuthOOB_UsesOpaqueResponseScope(t *testing.T) {
	sessions := NewManager(0, nil)
	persistedBeforeSession := false
	store := newScopeRejectTokenStore()
	store.beforePut = func() {
		persistedBeforeSession = len(sessions.ActiveSessions()) == 0
	}
	cfgPath := writeOAuthClientConfig(t)
	ext := newAuthExtension(&Config{
		CookieName: "agently_session",
		OAuth: &OAuth{
			Name: "oauth",
			Client: &OAuthClient{
				ConfigURL: cfgPath,
				Scopes:    []string{"openid", "profile"},
			},
		},
	}, sessions, "", store, nil)
	ext.oauthOOB = &staticOOBAuthorizer{
		token: (&oauth2.Token{
			AccessToken:  "opaque-oob-access",
			RefreshToken: "opaque-oob-refresh",
			Expiry:       time.Now().Add(time.Hour),
		}).WithExtra(map[string]interface{}{"scope": "openid profile"}),
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/api/auth/oob", strings.NewReader(`{
		"secretsURL": "file:///tmp/oauth-secret",
		"scopes": ["openid", "profile"]
	}`))
	rec := httptest.NewRecorder()

	ext.handleOAuthOOB().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var response struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	session := sessions.Get(context.Background(), response.SessionID)
	if session == nil || session.Tokens == nil || session.Tokens.AccessToken != "opaque-oob-access" {
		t.Fatalf("opaque OOB token was not stored in session: %#v", session)
	}
	select {
	case <-store.putCalled:
	default:
		t.Fatal("OOB token was not persisted synchronously")
	}
	if !persistedBeforeSession {
		t.Fatal("OOB session was exposed before token persistence")
	}
}

func TestAuthExtensionOAuthOOB_RejectsUnderScopedOpaqueResponse(t *testing.T) {
	sessions := NewManager(0, nil)
	store := newScopeRejectTokenStore()
	cfgPath := writeOAuthClientConfig(t)
	ext := newAuthExtension(&Config{
		CookieName: "agently_session",
		OAuth: &OAuth{
			Name: "oauth",
			Client: &OAuthClient{
				ConfigURL: cfgPath,
				Scopes:    []string{"openid", "profile"},
			},
		},
	}, sessions, "", store, nil)
	ext.oauthOOB = &staticOOBAuthorizer{
		token: (&oauth2.Token{
			AccessToken:  "under-scoped-oob-access",
			RefreshToken: "under-scoped-oob-refresh",
			Expiry:       time.Now().Add(time.Hour),
		}).WithExtra(map[string]interface{}{"scope": "openid"}),
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/api/auth/oob", strings.NewReader(`{
		"secretsURL": "file:///tmp/oauth-secret",
		"scopes": ["openid", "profile"]
	}`))
	rec := httptest.NewRecorder()

	ext.handleOAuthOOB().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusUnauthorized, rec.Body.String())
	}
	assertNoRejectedTokenPersistence(t, sessions, store, rec)
}

func TestAuthExtensionOAuthOOB_TokenPersistenceFailurePreventsSessionExposure(t *testing.T) {
	sessions := NewManager(0, nil)
	store := newScopeRejectTokenStore()
	store.putErr = errors.New("token-store failure")
	cfgPath := writeOAuthClientConfig(t)
	ext := newAuthExtension(&Config{
		CookieName: "agently_session",
		OAuth: &OAuth{
			Name: "oauth",
			Client: &OAuthClient{
				ConfigURL: cfgPath,
				Scopes:    []string{"openid", "profile"},
			},
		},
	}, sessions, "", store, nil)
	ext.oauthOOB = &staticOOBAuthorizer{
		token: (&oauth2.Token{
			AccessToken:  "opaque-oob-access",
			RefreshToken: "opaque-oob-refresh",
			Expiry:       time.Now().Add(time.Hour),
		}).WithExtra(map[string]interface{}{"scope": "openid profile"}),
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/api/auth/oob", strings.NewReader(`{
		"secretsURL": "file:///tmp/oauth-secret",
		"scopes": ["openid", "profile"]
	}`))
	rec := httptest.NewRecorder()

	ext.handleOAuthOOB().ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusInternalServerError, rec.Body.String())
	}
	assertNoSessionExposure(t, sessions, rec)
	select {
	case <-store.putCalled:
	default:
		t.Fatal("OOB token persistence was not attempted")
	}
}

func TestAuthExtensionOAuthCallback_UsesOpaqueResponseScope(t *testing.T) {
	sessions := NewManager(0, nil)
	persistedBeforeSession := false
	store := newScopeRejectTokenStore()
	store.beforePut = func() {
		persistedBeforeSession = len(sessions.ActiveSessions()) == 0
	}
	cfgPath := writeOAuthClientConfigWithTokenURL(t, "https://token.example.test/oauth/token")
	ext := newAuthExtension(&Config{
		CookieName:   "agently_session",
		RedirectPath: "/v1/api/auth/oauth/callback",
		OAuth: &OAuth{
			Name: "oauth",
			Client: &OAuthClient{
				ConfigURL: cfgPath,
				Scopes:    []string{"openid", "profile"},
			},
		},
	}, sessions, "", store, nil)
	state, err := encryptOAuthState(context.Background(), cfgPath, oauthStatePayload{
		CodeVerifier: "verifier",
		Scopes:       []string{"openid", "profile"},
	})
	if err != nil {
		t.Fatalf("encryptOAuthState() error = %v", err)
	}
	httpClient := oauthCallbackScopeHTTPClient(`{
		"access_token": "opaque-callback-access",
		"refresh_token": "opaque-callback-refresh",
		"token_type": "Bearer",
		"expires_in": 3600,
		"scope": "openid profile"
	}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/api/auth/oauth/callback", strings.NewReader(`{
		"code": "code",
		"state": "`+state+`"
	}`))
	req = req.WithContext(context.WithValue(req.Context(), oauth2.HTTPClient, httpClient))
	rec := httptest.NewRecorder()

	ext.handleOAuthCallback().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var response struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	session := sessions.Get(context.Background(), response.SessionID)
	if session == nil || session.Tokens == nil || session.Tokens.AccessToken != "opaque-callback-access" {
		t.Fatalf("opaque callback token was not stored in session: %#v", session)
	}
	select {
	case <-store.putCalled:
	default:
		t.Fatal("callback token was not persisted synchronously")
	}
	if !persistedBeforeSession {
		t.Fatal("callback session was exposed before token persistence")
	}
}

func TestAuthExtensionOAuthCallback_OwnerPersistenceFailurePreventsSessionExposure(t *testing.T) {
	sessions := NewManager(0, nil)
	store := newScopeRejectTokenStore()
	users := &failingOAuthPersistUserService{err: errors.New("owner persistence failure")}
	cfgPath := writeOAuthClientConfigWithTokenURL(t, "https://token.example.test/oauth/token")
	ext := newAuthExtension(&Config{
		CookieName:   "agently_session",
		RedirectPath: "/v1/api/auth/oauth/callback",
		OAuth: &OAuth{
			Name: "oauth",
			Client: &OAuthClient{
				ConfigURL: cfgPath,
				Scopes:    []string{"openid", "profile"},
			},
		},
	}, sessions, "", store, users)
	state, err := encryptOAuthState(context.Background(), cfgPath, oauthStatePayload{
		CodeVerifier: "verifier",
		Scopes:       []string{"openid", "profile"},
	})
	if err != nil {
		t.Fatalf("encryptOAuthState() error = %v", err)
	}
	httpClient := oauthCallbackScopeHTTPClient(`{
		"access_token": "opaque-callback-access",
		"refresh_token": "opaque-callback-refresh",
		"token_type": "Bearer",
		"expires_in": 3600,
		"scope": "openid profile"
	}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/api/auth/oauth/callback", strings.NewReader(`{
		"code": "code",
		"state": "`+state+`"
	}`))
	req = req.WithContext(context.WithValue(req.Context(), oauth2.HTTPClient, httpClient))
	rec := httptest.NewRecorder()

	ext.handleOAuthCallback().ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusInternalServerError, rec.Body.String())
	}
	assertNoRejectedTokenPersistence(t, sessions, store, rec)
}

func TestAuthExtensionOAuthCallback_RejectsUnderScopedOpaqueResponse(t *testing.T) {
	sessions := NewManager(0, nil)
	store := newScopeRejectTokenStore()
	cfgPath := writeOAuthClientConfigWithTokenURL(t, "https://token.example.test/oauth/token")
	ext := newAuthExtension(&Config{
		CookieName:   "agently_session",
		RedirectPath: "/v1/api/auth/oauth/callback",
		OAuth: &OAuth{
			Name: "oauth",
			Client: &OAuthClient{
				ConfigURL: cfgPath,
				Scopes:    []string{"openid", "profile"},
			},
		},
	}, sessions, "", store, nil)
	state, err := encryptOAuthState(context.Background(), cfgPath, oauthStatePayload{
		CodeVerifier: "verifier",
		Scopes:       []string{"openid", "profile"},
	})
	if err != nil {
		t.Fatalf("encryptOAuthState() error = %v", err)
	}
	httpClient := oauthCallbackScopeHTTPClient(`{
		"access_token": "under-scoped-callback-access",
		"refresh_token": "under-scoped-callback-refresh",
		"token_type": "Bearer",
		"expires_in": 3600,
		"scope": "openid"
	}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/api/auth/oauth/callback", strings.NewReader(`{
		"code": "code",
		"state": "`+state+`"
	}`))
	req = req.WithContext(context.WithValue(req.Context(), oauth2.HTTPClient, httpClient))
	rec := httptest.NewRecorder()

	ext.handleOAuthCallback().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusUnauthorized, rec.Body.String())
	}
	assertNoRejectedTokenPersistence(t, sessions, store, rec)
}

func oauthCallbackScopeHTTPClient(responseBody string) *http.Client {
	return &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(responseBody)),
			Request:    req,
		}, nil
	})}
}
