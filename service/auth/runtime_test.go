package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	iauth "github.com/viant/agently-core/internal/auth"
	"github.com/viant/scy"
	scyauth "github.com/viant/scy/auth"
	"github.com/viant/scy/auth/jwt/signer"
	"golang.org/x/oauth2"
)

func TestWithRuntimeAuthUserBridgesCoreContexts(t *testing.T) {
	tokens := &scyauth.Token{}
	tokens.Token.AccessToken = "access-token"

	ctx := withRuntimeAuthUser(context.Background(), &runtimeAuthUser{
		EffectiveUserID: "user-canonical",
		Subject:         "devuser",
		Email:           "devuser@example.com",
		Provider:        "oauth",
		Tokens:          tokens,
	})

	if got := EffectiveUserID(ctx); got != "user-canonical" {
		t.Fatalf("auth effective user = %q, want %q", got, "user-canonical")
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
	for store.upserts.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if store.upserts.Load() == 0 {
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
		Username: "localuser",
		Subject:  "localuser",
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

func TestRuntimeProtect_KeepsSubjectForRequestOwnership(t *testing.T) {
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
				OAuth:      &OAuth{Name: "oauth", Mode: "bff"},
			},
			users: canonicalLookupUserService{},
		},
	}
	rt.sessions.Put(nil, &Session{
		ID:       "sess-1",
		Username: "localuser",
		Subject:  "oauth_subject_test",
		Provider: "oauth",
		Tokens:   newTokenBundle("access-token", "id-token", ""),
	})

	handler := rt.protectAll(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(EffectiveUserID(r.Context())))
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/conversations", nil)
	req.AddCookie(&http.Cookie{Name: "agently_session", Value: "sess-1"})
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if got := strings.TrimSpace(rec.Body.String()); got != "oauth_subject_test" {
		t.Fatalf("effective user = %q, want %q", got, "oauth_subject_test")
	}
	if sess := rt.sessions.Get(context.Background(), "sess-1"); sess == nil || strings.TrimSpace(sess.UserID) != "user-canonical" {
		t.Fatalf("session canonical user not updated, got %#v", sess)
	}
}

func TestRuntimeProtectAll_JWTBearer_PopulatesIDToken(t *testing.T) {
	keyDir := t.TempDir()
	privPath, pubPath := generateRSAKeyPair(t, keyDir)

	rt := &Runtime{
		cfg: &Config{
			Enabled:    true,
			CookieName: "agently_session",
			IpHashKey:  "test-ip-hash-key",
			OAuth:      &OAuth{Mode: "bff"},
			JWT: &JWT{
				Enabled: true,
				RSA:     []string{pubPath},
			},
		},
		sessions: NewManager(0, nil),
	}
	jwtSvc := NewJWTService(rt.cfg.JWT)
	require.NoError(t, jwtSvc.Init(context.Background()))
	rt.jwtService = jwtSvc
	rt.jwtVerifier = jwtSvc.verifier

	token := signTestJWT(t, privPath, map[string]interface{}{
		"sub":   "jwt-idtoken-user",
		"email": "jwt-idtoken@example.com",
	}, 1*time.Hour)

	handler := rt.protectAll(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(iauth.IDToken(r.Context())))
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/conversations", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, token, strings.TrimSpace(rec.Body.String()))
}

func TestRuntimeProtect_TransientRefreshFailureDoesNotDeleteSession(t *testing.T) {
	store := &sessionStoreContextProbe{}
	rt := &Runtime{
		cfg: &Config{
			Enabled:    true,
			CookieName: "agently_session",
			IpHashKey:  "dev-hmac-salt",
			OAuth:      &OAuth{Mode: "bff"},
		},
		sessions: NewManager(0, store),
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
		Username: "oauth_subject_test",
		Subject:  "oauth_subject_test",
		Provider: "oauth",
		Tokens:   tokens,
	})

	var downstreamTokens *scyauth.Token
	handler := rt.protectAll(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		downstreamTokens = iauth.TokensFromContext(r.Context())
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
	if last := store.lastUpsertErr(); last != nil {
		t.Fatalf("expected transient refresh retry persistence to use live context, got %v", last)
	}
	if got := store.upsertCount(); got < 2 {
		t.Fatalf("expected initial session put and retry persistence, got %d upserts", got)
	}
	if downstreamTokens != nil {
		t.Fatalf("expired token reached downstream context: %#v", downstreamTokens)
	}
}

func TestRuntimeProtect_UnderScopedStoredSessionIsPreservedButNotInjected(t *testing.T) {
	underScoped := fakeJWTWithClaims(t, map[string]any{"scope": "openid"})
	cfg := &Config{
		Enabled:    true,
		CookieName: "agently_session",
		IpHashKey:  "dev-hmac-salt",
		OAuth: &OAuth{
			Name: "oauth",
			Mode: "bff",
			Client: &OAuthClient{
				Scopes: []string{"openid", "ROLE_STEWARD_WEB"},
			},
		},
	}
	sessions := NewManager(time.Hour, nil)
	rt := &Runtime{
		cfg:      cfg,
		sessions: sessions,
		ext:      &authExtension{cfg: cfg, sessions: sessions},
	}
	sessions.Put(context.Background(), &Session{
		ID:       "sess-under-scoped",
		UserID:   "user-42",
		Subject:  "provider-subject",
		Provider: "oauth",
		Scopes:   []string{"openid", "ROLE_STEWARD_WEB"},
		Tokens: &scyauth.Token{
			Token: oauth2.Token{
				AccessToken:  underScoped,
				RefreshToken: "refresh-token",
				Expiry:       time.Now().Add(time.Hour),
			},
		},
	})

	var downstreamTokens *scyauth.Token
	handler := rt.protectAll(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		downstreamTokens = iauth.TokensFromContext(req.Context())
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/v1/conversations", nil)
	req.AddCookie(&http.Cookie{Name: cfg.CookieName, Value: "sess-under-scoped"})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if downstreamTokens != nil {
		t.Fatalf("under-scoped token reached downstream context: %#v", downstreamTokens)
	}
	if got := sessions.Get(context.Background(), "sess-under-scoped"); got == nil || got.Tokens == nil {
		t.Fatalf("under-scoped session credentials were not preserved: %#v", got)
	}
}

func TestRuntimeProtect_TransientRefreshFailurePersistsWithCanceledRequestContext(t *testing.T) {
	store := &sessionStoreContextProbe{}
	rt := &Runtime{
		cfg: &Config{
			Enabled:    true,
			CookieName: "agently_session",
			IpHashKey:  "dev-hmac-salt",
			OAuth:      &OAuth{Mode: "bff"},
		},
		sessions: NewManager(0, store),
		ext: &authExtension{
			cfg: &Config{
				Enabled:    true,
				CookieName: "agently_session",
				OAuth:      &OAuth{Mode: "bff"},
			},
		},
	}

	tokens := &scyauth.Token{}
	tokens.Token.AccessToken = "expired-access"
	tokens.Token.RefreshToken = "refresh-token"
	tokens.Token.Expiry = time.Now().Add(-1 * time.Minute)

	rt.sessions.Put(context.Background(), &Session{
		ID:       "sess-expired-canceled",
		Username: "oauth_subject_test",
		Subject:  "oauth_subject_test",
		Provider: "oauth",
		Tokens:   tokens,
	})

	handler := rt.protectAll(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodGet, "/v1/conversations", nil).WithContext(ctx)
	req.AddCookie(&http.Cookie{Name: "agently_session", Value: "sess-expired-canceled"})
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if got := rt.sessions.Get(context.Background(), "sess-expired-canceled"); got == nil {
		t.Fatalf("expected session to be preserved after transient refresh failure")
	} else if got.TransientRefreshRetryAt.IsZero() {
		t.Fatalf("expected preserved session to carry transient refresh cooldown")
	}
	if last := store.lastUpsertErr(); last != nil {
		t.Fatalf("expected retry persistence to ignore canceled request context, got %v", last)
	}
	if got := store.upsertCount(); got < 2 {
		t.Fatalf("expected initial session put and retry persistence, got %d upserts", got)
	}
}

type sessionStoreContextProbe struct {
	mu         sync.Mutex
	upsertErrs []error
	deleteErrs []error
}

func (s *sessionStoreContextProbe) Get(_ context.Context, _ string) (*SessionRecord, error) {
	return nil, nil
}

func (s *sessionStoreContextProbe) Upsert(ctx context.Context, _ *SessionRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ctx == nil {
		s.upsertErrs = append(s.upsertErrs, nil)
		return nil
	}
	s.upsertErrs = append(s.upsertErrs, ctx.Err())
	return nil
}

func (s *sessionStoreContextProbe) Delete(ctx context.Context, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ctx == nil {
		s.deleteErrs = append(s.deleteErrs, nil)
		return nil
	}
	s.deleteErrs = append(s.deleteErrs, ctx.Err())
	return nil
}

func (s *sessionStoreContextProbe) lastUpsertErr() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.upsertErrs) == 0 {
		return nil
	}
	return s.upsertErrs[len(s.upsertErrs)-1]
}

func (s *sessionStoreContextProbe) upsertCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.upsertErrs)
}

func generateRSAKeyPair(t *testing.T, dir string) (privPath, pubPath string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	privPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
	pubDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	require.NoError(t, err)
	pubPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: pubDER,
	})

	privPath = filepath.Join(dir, "private.pem")
	pubPath = filepath.Join(dir, "public.pem")
	require.NoError(t, os.WriteFile(privPath, privPEM, 0o600))
	require.NoError(t, os.WriteFile(pubPath, pubPEM, 0o644))
	return
}

func signTestJWT(t *testing.T, privPath string, claims map[string]interface{}, ttl time.Duration) string {
	t.Helper()
	cfg := &signer.Config{
		RSA: scy.NewResource("", privPath, ""),
	}
	s := signer.New(cfg)
	require.NoError(t, s.Init(context.Background()))
	token, err := s.Create(ttl, claims)
	require.NoError(t, err)
	return token
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
		Username:                "oauth_subject_test",
		Subject:                 "oauth_subject_test",
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
	httpClient := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		hits.Add(1)
		select {
		case <-req.Context().Done():
			return nil, req.Context().Err()
		case <-time.After(300 * time.Millisecond):
			return &http.Response{
				StatusCode: http.StatusGatewayTimeout,
				Header:     http.Header{"Content-Type": []string{"text/plain"}},
				Body:       io.NopCloser(strings.NewReader("slow token endpoint")),
				Request:    req,
			}, nil
		}
	})}

	cfgDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "oauth.json")
	payload := map[string]any{
		"authURL":      "https://token.example.test/auth",
		"tokenURL":     "https://token.example.test/token",
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
		Username: "localuser",
		Subject:  "oauth_subject_test",
		Provider: "oauth",
		Tokens:   tokens,
	})

	handler := rt.protect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	ctx = context.WithValue(ctx, oauth2.HTTPClient, httpClient)
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
		Subject:  "oauth_subject_test",
		Provider: "oauth",
	}
	rt.storeRefreshRetryAt(seed, until)

	reloaded := &Session{
		ID:       "sess-b",
		Subject:  "oauth_subject_test",
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
	if got := rt.loadRefreshRetryAt(&Session{Subject: "oauth_subject_test", Provider: "oauth"}); !got.IsZero() {
		t.Fatalf("expected cleared retry timestamp, got %v", got)
	}
}

func TestRuntimeShouldLogRefreshRetry_LogsOncePerCooldownWindow(t *testing.T) {
	rt := &Runtime{}
	sess := &Session{Subject: "oauth_subject_test", Provider: "oauth"}
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
