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

// TestAuthExtensionPersistOAuthToken_UsesJWTSubAsUserID verifies that the token
// is stored under jwt.sub (not the DB canonical user ID). The users table is
// updated for display purposes but must not override the storage key.
func TestAuthExtensionPersistOAuthToken_UsesJWTSubAsUserID(t *testing.T) {
	store := &testTokenStore{}
	users := &testUserService{userID: "user-42"} // DB canonical ID — must NOT be the storage key
	ext := &authExtension{
		cfg:        &Config{OAuth: &OAuth{Name: "oauth"}},
		sessions:   NewManager(0, nil),
		tokenStore: store,
		users:      users,
	}

	ext.persistOAuthToken(context.Background(), "oauth_callback", "ppoudyal", "ppoudyal@viantinc.com", "user-sub-123", "oauth", "access", "id", "refresh", time.Now().Add(time.Hour))

	// Token must be stored under jwt.sub, not the DB canonical ID.
	if store.putUser != "user-sub-123" {
		t.Fatalf("persisted token user = %q, want jwt.sub %q", store.putUser, "user-sub-123")
	}
}

// TestAuthExtensionEnsureSessionOAuthTokens_UsesJWTSub verifies that session
// token rehydration uses sess.Subject (jwt.sub) directly as the token store key.
func TestAuthExtensionEnsureSessionOAuthTokens_UsesJWTSub(t *testing.T) {
	store := &testTokenStore{
		token: &OAuthToken{
			Username:     "user-sub-123", // stored under jwt.sub
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
	if store.getUser != "user-sub-123" {
		t.Fatalf("token lookup user = %q, want jwt.sub %q", store.getUser, "user-sub-123")
	}
	if sess.Tokens == nil || sess.Tokens.AccessToken != "access" {
		t.Fatalf("expected session tokens to be rehydrated")
	}
	if sess.Provider != "oauth" {
		t.Fatalf("session provider = %q, want %q", sess.Provider, "oauth")
	}
}

// TestRuntimeResolveRuntimeOAuthTokenOwner_UsesJWTSub verifies that the token
// owner is resolved directly from sess.Subject (jwt.sub) without a DB lookup.
func TestRuntimeResolveRuntimeOAuthTokenOwner_UsesJWTSub(t *testing.T) {
	rt := &Runtime{
		ext: &authExtension{
			cfg: &Config{OAuth: &OAuth{Name: "oauth", Mode: "bff"}},
		},
	}
	sess := &Session{
		ID:       "sess-1",
		Username: "ppoudyal",
		Subject:  "user-sub-123",
		Provider: "oauth",
	}

	userID, provider := rt.resolveRuntimeOAuthTokenOwner(context.Background(), sess)
	if userID != "user-sub-123" {
		t.Fatalf("resolved userID = %q, want jwt.sub %q", userID, "user-sub-123")
	}
	if provider != "oauth" {
		t.Fatalf("resolved provider = %q, want %q", provider, "oauth")
	}
}

// TestRuntimeEnsureSessionOAuthTokens_UsesJWTSub verifies end-to-end that
// runtime session token rehydration uses jwt.sub as the lookup key.
func TestRuntimeEnsureSessionOAuthTokens_UsesJWTSub(t *testing.T) {
	store := &testTokenStore{
		token: &OAuthToken{
			Username:     "user-sub-123", // stored under jwt.sub
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
	if store.getUser != "user-sub-123" {
		t.Fatalf("token lookup user = %q, want jwt.sub %q", store.getUser, "user-sub-123")
	}
	if sess.Tokens == nil || sess.Tokens.AccessToken != "access" {
		t.Fatalf("expected session tokens to be rehydrated")
	}
}
