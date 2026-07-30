package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type cancelAwareUserService struct {
	userID                  string
	upsertWithProviderCalls atomic.Int32
}

func (c *cancelAwareUserService) GetByUsername(_ context.Context, username string) (*User, error) {
	return &User{ID: c.userID, Username: username}, nil
}

func (c *cancelAwareUserService) GetBySubjectAndProvider(_ context.Context, _, _ string) (*User, error) {
	return nil, nil
}

func (c *cancelAwareUserService) Upsert(_ context.Context, _ *User) error { return nil }

func (c *cancelAwareUserService) UpsertWithProvider(ctx context.Context, username, displayName, email, provider, subject string) (string, error) {
	c.upsertWithProviderCalls.Add(1)
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if c.userID == "" {
		c.userID = "user-ctx-ok"
	}
	return c.userID, nil
}

func (c *cancelAwareUserService) UpdateHashIPByID(_ context.Context, _, _ string) error { return nil }

func (c *cancelAwareUserService) UpdatePreferences(_ context.Context, _ string, _ *PreferencesPatch) error {
	return nil
}

type cancelAwareTokenStore struct {
	putUser string
}

func (c *cancelAwareTokenStore) Get(_ context.Context, _, _ string) (*OAuthToken, error) {
	return nil, nil
}

func (c *cancelAwareTokenStore) Put(ctx context.Context, token *OAuthToken) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.putUser = token.Username
	return nil
}

func (c *cancelAwareTokenStore) Delete(_ context.Context, _, _ string) error { return nil }

func (c *cancelAwareTokenStore) TryAcquireRefreshLease(_ context.Context, _, _, _ string, _ time.Duration) (int64, bool, error) {
	return 0, false, nil
}

func (c *cancelAwareTokenStore) ReleaseRefreshLease(_ context.Context, _, _, _ string) error {
	return nil
}

func (c *cancelAwareTokenStore) CASPut(_ context.Context, _ *OAuthToken, _ int64, _ string) (bool, error) {
	return false, nil
}

type blockingUserService struct {
	userID  string
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *blockingUserService) GetByUsername(_ context.Context, username string) (*User, error) {
	return &User{ID: b.userID, Username: username}, nil
}

func (b *blockingUserService) GetBySubjectAndProvider(_ context.Context, _, _ string) (*User, error) {
	return nil, nil
}

func (b *blockingUserService) Upsert(_ context.Context, _ *User) error { return nil }

func (b *blockingUserService) UpsertWithProvider(_ context.Context, username, displayName, email, provider, subject string) (string, error) {
	b.once.Do(func() {
		if b.started != nil {
			close(b.started)
		}
	})
	<-b.release
	if b.userID == "" {
		b.userID = "blocking-user"
	}
	return b.userID, nil
}

func (b *blockingUserService) UpdateHashIPByID(_ context.Context, _, _ string) error { return nil }

func (b *blockingUserService) UpdatePreferences(_ context.Context, _ string, _ *PreferencesPatch) error {
	return nil
}

type blockingSessionStore struct {
	release chan struct{}
	upserts atomic.Int64
}

func (b *blockingSessionStore) Get(_ context.Context, _ string) (*SessionRecord, error) {
	return nil, nil
}

func (b *blockingSessionStore) Upsert(_ context.Context, _ *SessionRecord) error {
	b.upserts.Add(1)
	<-b.release
	return nil
}

func (b *blockingSessionStore) Delete(_ context.Context, _ string) error { return nil }

func TestRuntimeHandleCreateSession_TokenBackedWithoutStoreSkipsOwnerPersist(t *testing.T) {
	users := &cancelAwareUserService{userID: "must-not-be-used"}
	sessions := NewManager(time.Hour, nil)
	ext := &authExtension{
		cfg: &Config{
			CookieName: "agently_session",
			OAuth:      &OAuth{Name: "oauth", Mode: "bff"},
		},
		sessions: sessions,
		users:    users,
	}
	idToken := "x." + base64.RawURLEncoding.EncodeToString([]byte(
		`{"sub":"user-123","preferred_username":"devuser","exp":4102444800}`,
	)) + ".y"
	req := httptest.NewRequest(http.MethodPost, "/v1/api/auth/session", strings.NewReader(
		`{"username":"devuser","idToken":"`+idToken+`","accessToken":"token-access"}`,
	))
	rec := httptest.NewRecorder()

	ext.handleCreateSession().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if got := users.upsertWithProviderCalls.Load(); got != 0 {
		t.Fatalf("UserService.UpsertWithProvider calls = %d, want 0", got)
	}
	active := sessions.ActiveSessions()
	if len(active) != 1 || active[0].Tokens == nil || active[0].Tokens.AccessToken != "token-access" {
		t.Fatalf("published sessions = %#v, want one token-backed session", active)
	}
	foundCookie := false
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == "agently_session" && cookie.Value == active[0].ID {
			foundCookie = true
		}
	}
	if !foundCookie {
		t.Fatal("session cookie was not published")
	}
}

func TestRuntimeHandleCreateSession_PersistsOAuthTokenWithDurableContext(t *testing.T) {
	store := &cancelAwareTokenStore{}
	users := &cancelAwareUserService{userID: "user-42"}
	ext := &authExtension{
		cfg: &Config{
			CookieName: "agently_session",
			OAuth:      &OAuth{Name: "oauth", Mode: "bff"},
		},
		sessions:   NewManager(time.Hour, nil),
		tokenStore: store,
		users:      users,
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

	parentCtx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodPost, "/v1/api/auth/session", strings.NewReader(
		`{"username":"devuser","idToken":"`+idToken+`","accessToken":"token-access"}`,
	)).WithContext(parentCtx)
	rec := httptest.NewRecorder()

	ext.handleCreateSession().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	deadline := time.Now().Add(time.Second)
	for store.putUser == "" && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if store.putUser != "user-42" {
		t.Fatalf("persisted token user = %q, want %q", store.putUser, "user-42")
	}
}

func TestRuntimeHandleCreateSession_PersistsOAuthBeforePublishingSession(t *testing.T) {
	users := &blockingUserService{userID: "user-77", started: make(chan struct{}), release: make(chan struct{})}
	store := &cancelAwareTokenStore{}
	sessionStore := &blockingSessionStore{release: make(chan struct{})}
	var usersReleaseOnce sync.Once
	var sessionReleaseOnce sync.Once
	releaseUsers := func() { usersReleaseOnce.Do(func() { close(users.release) }) }
	releaseSessionStore := func() { sessionReleaseOnce.Do(func() { close(sessionStore.release) }) }
	t.Cleanup(releaseUsers)
	t.Cleanup(releaseSessionStore)
	ext := &authExtension{
		cfg: &Config{
			CookieName: "agently_session",
			OAuth:      &OAuth{Name: "oauth", Mode: "bff"},
		},
		sessions:   NewManager(time.Hour, sessionStore),
		tokenStore: store,
		users:      users,
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
	done := make(chan struct{})
	go func() {
		defer close(done)
		ext.handleCreateSession().ServeHTTP(rec, req)
	}()

	select {
	case <-users.started:
	case <-time.After(time.Second):
		t.Fatal("oauth persistence did not start")
	}
	select {
	case <-done:
		t.Fatal("session was published before oauth persistence completed")
	default:
	}
	if active := ext.sessions.ActiveSessions(); len(active) != 0 {
		t.Fatalf("sessions published before oauth persistence: %#v", active)
	}
	if headers := rec.Header().Values("Set-Cookie"); len(headers) != 0 {
		t.Fatalf("cookie published before oauth persistence: %#v", headers)
	}
	releaseUsers()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("session create did not complete after oauth persistence")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if active := ext.sessions.ActiveSessions(); len(active) != 1 {
		t.Fatalf("sessions after oauth persistence = %#v, want one", active)
	}
	releaseSessionStore()
	if cookies := rec.Result().Cookies(); len(cookies) == 0 {
		t.Fatal("session cookie missing after oauth persistence")
	}
}

func TestRuntimeHandleCreateSession_PersistenceFailureDoesNotExposeSessionOrCookie(t *testing.T) {
	tests := []struct {
		name    string
		users   UserService
		putErr  error
		wantPut bool
	}{
		{
			name:  "owner",
			users: &failingOAuthPersistUserService{err: context.DeadlineExceeded},
		},
		{
			name:    "token store",
			users:   &cancelAwareUserService{userID: "user-77"},
			putErr:  context.DeadlineExceeded,
			wantPut: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sessions := NewManager(time.Hour, nil)
			store := newScopeRejectTokenStore()
			store.putErr = test.putErr
			ext := &authExtension{
				cfg: &Config{
					CookieName: "agently_session",
					OAuth:      &OAuth{Name: "oauth", Mode: "bff"},
				},
				sessions:   sessions,
				tokenStore: store,
				users:      test.users,
			}
			idToken := "x." + base64.RawURLEncoding.EncodeToString([]byte(
				`{"sub":"user-123","preferred_username":"devuser","exp":4102444800}`,
			)) + ".y"
			req := httptest.NewRequest(http.MethodPost, "/v1/api/auth/session", strings.NewReader(
				`{"username":"devuser","idToken":"`+idToken+`","accessToken":"token-access"}`,
			))
			rec := httptest.NewRecorder()

			ext.handleCreateSession().ServeHTTP(rec, req)

			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
			}
			assertNoSessionExposure(t, sessions, rec)
			select {
			case <-store.putCalled:
				if !test.wantPut {
					t.Fatal("token store called after owner failure")
				}
			default:
				if test.wantPut {
					t.Fatal("token store failure path did not call Put")
				}
			}
		})
	}
}
