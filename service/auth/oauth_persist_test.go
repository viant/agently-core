package auth

import (
	"context"
	"testing"
	"time"
)

type testUserService struct {
	userID                string
	lastGetName           string
	lastSubject           string
	lastProvider          string
	userBySubjectProvider map[string]*User
}

func (t *testUserService) GetByUsername(_ context.Context, username string) (*User, error) {
	t.lastGetName = username
	if t.userID == "" {
		return nil, nil
	}
	return &User{ID: t.userID, Username: username}, nil
}

func (t *testUserService) GetBySubjectAndProvider(_ context.Context, subject, provider string) (*User, error) {
	t.lastSubject = subject
	t.lastProvider = provider
	if t.userBySubjectProvider == nil {
		return nil, nil
	}
	return t.userBySubjectProvider[subject+"|"+provider], nil
}

func (t *testUserService) Upsert(_ context.Context, _ *User) error { return nil }

func (t *testUserService) UpsertWithProvider(_ context.Context, username, displayName, email, provider, subject string) (string, error) {
	if t.userID == "" {
		t.userID = "user-1"
	}
	return t.userID, nil
}

func (t *testUserService) UpdateHashIPByID(_ context.Context, _, _ string) error { return nil }

func (t *testUserService) UpdatePreferences(_ context.Context, _ string, _ *PreferencesPatch) error {
	return nil
}

type testTokenStore struct {
	putUser string
	getUser string
	token   *OAuthToken
}

func (t *testTokenStore) Get(_ context.Context, username, _ string) (*OAuthToken, error) {
	t.getUser = username
	return t.token, nil
}

func (t *testTokenStore) Put(_ context.Context, token *OAuthToken) error {
	t.putUser = token.Username
	return nil
}

func (t *testTokenStore) Delete(_ context.Context, _, _ string) error { return nil }

func (t *testTokenStore) TryAcquireRefreshLease(_ context.Context, _, _, _ string, _ time.Duration) (int64, bool, error) {
	return 0, false, nil
}

func (t *testTokenStore) ReleaseRefreshLease(_ context.Context, _, _, _ string) error { return nil }

func (t *testTokenStore) CASPut(_ context.Context, _ *OAuthToken, _ int64, _ string) (bool, error) {
	return false, nil
}

// TestAuthExtensionPersistOAuthToken_UsesCanonicalUserID verifies that token
// persistence uses the canonical DB user ID returned by the user service.
func TestAuthExtensionPersistOAuthToken_UsesCanonicalUserID(t *testing.T) {
	store := &testTokenStore{}
	users := &testUserService{userID: "user-42"}
	ext := &authExtension{
		cfg:        &Config{OAuth: &OAuth{Name: "oauth"}},
		sessions:   NewManager(0, nil),
		tokenStore: store,
		users:      users,
	}

	ext.persistOAuthToken(context.Background(), "oauth_callback", "ppoudyal", "ppoudyal@viantinc.com", "user-sub-123", "oauth", "access", "id", "refresh", time.Now().Add(time.Hour))

	if store.putUser != "user-42" {
		t.Fatalf("persisted token user = %q, want canonical user ID %q", store.putUser, "user-42")
	}
}

// TestAuthExtensionEnsureSessionOAuthTokens_UsesSubjectProviderMapping verifies
// that session token rehydration resolves the canonical DB user ID from the
// stable oauth subject/provider mapping before any username fallback.
func TestAuthExtensionEnsureSessionOAuthTokens_UsesSubjectProviderMapping(t *testing.T) {
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
		cfg:        &Config{OAuth: &OAuth{Name: "oauth", Mode: "bff"}},
		sessions:   NewManager(0, nil),
		tokenStore: store,
		users: &testUserService{userBySubjectProvider: map[string]*User{
			"user-sub-123|oauth": {ID: "user-42", Username: "ppoudyal"},
		}},
	}
	sess := &Session{
		ID:        "sess-1",
		Username:  "ppoudyal",
		Subject:   "user-sub-123",
		Provider:  "oauth",
		CreatedAt: time.Now(),
	}

	ok := ext.ensureSessionOAuthTokens(context.Background(), sess)
	if !ok {
		t.Fatalf("ensureSessionOAuthTokens() = false, want true")
	}
	if store.getUser != "user-42" {
		t.Fatalf("token lookup user = %q, want canonical user ID %q", store.getUser, "user-42")
	}
	if got := ext.users.(*testUserService).lastSubject; got != "user-sub-123" {
		t.Fatalf("subject lookup = %q, want %q", got, "user-sub-123")
	}
	if got := ext.users.(*testUserService).lastProvider; got != "oauth" {
		t.Fatalf("provider lookup = %q, want %q", got, "oauth")
	}
	if got := ext.users.(*testUserService).lastGetName; got != "" {
		t.Fatalf("username fallback unexpectedly used = %q", got)
	}
	if sess.Tokens == nil || sess.Tokens.AccessToken != "access" {
		t.Fatalf("expected session tokens to be rehydrated")
	}
	if sess.Provider != "oauth" {
		t.Fatalf("session provider = %q, want %q", sess.Provider, "oauth")
	}
}

// TestRuntimeResolveRuntimeOAuthTokenOwner_UsesSubjectProviderMapping verifies
// that runtime token ownership resolves from subject/provider before username
// fallback.
func TestRuntimeResolveRuntimeOAuthTokenOwner_UsesSubjectProviderMapping(t *testing.T) {
	users := &testUserService{userBySubjectProvider: map[string]*User{
		"user-sub-123|oauth": {ID: "user-42", Username: "ppoudyal"},
	}}
	rt := &Runtime{
		ext: &authExtension{
			cfg:   &Config{OAuth: &OAuth{Name: "oauth", Mode: "bff"}},
			users: users,
		},
	}
	sess := &Session{
		ID:       "sess-1",
		Username: "ppoudyal",
		Subject:  "user-sub-123",
		Provider: "oauth",
	}

	userID, provider := rt.resolveRuntimeOAuthTokenOwner(context.Background(), sess)
	if userID != "user-42" {
		t.Fatalf("resolved userID = %q, want canonical user ID %q", userID, "user-42")
	}
	if provider != "oauth" {
		t.Fatalf("resolved provider = %q, want %q", provider, "oauth")
	}
	if users.lastSubject != "user-sub-123" {
		t.Fatalf("subject lookup = %q, want %q", users.lastSubject, "user-sub-123")
	}
	if users.lastGetName != "" {
		t.Fatalf("username fallback unexpectedly used = %q", users.lastGetName)
	}
}

// TestRuntimeEnsureSessionOAuthTokens_UsesSubjectProviderMapping verifies
// end-to-end runtime session token rehydration via subject/provider mapping.
func TestRuntimeEnsureSessionOAuthTokens_UsesSubjectProviderMapping(t *testing.T) {
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
	rt := &Runtime{
		sessions: NewManager(0, nil),
		cfg:      &Config{OAuth: &OAuth{Name: "oauth", Mode: "bff"}},
		ext: &authExtension{
			cfg:        &Config{OAuth: &OAuth{Name: "oauth", Mode: "bff"}},
			sessions:   NewManager(0, nil),
			tokenStore: store,
			users: &testUserService{userBySubjectProvider: map[string]*User{
				"user-sub-123|oauth": {ID: "user-42", Username: "ppoudyal"},
			}},
		},
	}
	sess := &Session{
		ID:        "sess-1",
		Username:  "ppoudyal",
		Subject:   "user-sub-123",
		Provider:  "oauth",
		CreatedAt: time.Now(),
	}

	ok := rt.ensureSessionOAuthTokens(context.Background(), sess)
	if !ok {
		t.Fatalf("ensureSessionOAuthTokens() = false, want true")
	}
	if store.getUser != "user-42" {
		t.Fatalf("token lookup user = %q, want canonical user ID %q", store.getUser, "user-42")
	}
	if sess.Tokens == nil || sess.Tokens.AccessToken != "access" {
		t.Fatalf("expected session tokens to be rehydrated")
	}
}
