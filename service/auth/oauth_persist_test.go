package auth

import (
	"context"
	"testing"
	"time"
)

type testUserService struct {
	userID      string
	lastGetName string
}

func (t *testUserService) GetByUsername(_ context.Context, username string) (*User, error) {
	t.lastGetName = username
	if t.userID == "" {
		return nil, nil
	}
	return &User{ID: t.userID, Username: username}, nil
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

func TestAuthExtensionPersistOAuthToken_UsesProvisionedUserID(t *testing.T) {
	store := &testTokenStore{}
	users := &testUserService{userID: "user-42"}
	ext := &authExtension{
		cfg:        &Config{OAuth: &OAuth{Name: "oauth"}},
		sessions:   NewManager(0, nil),
		tokenStore: store,
		users:      users,
	}

	ext.persistOAuthToken(context.Background(), "oauth_callback", "ppoudyal", "ppoudyal@viantinc.com", "agently_scheduler", "oauth", "access", "id", "refresh", time.Now().Add(time.Hour))

	if store.putUser != "user-42" {
		t.Fatalf("persisted token user = %q, want %q", store.putUser, "user-42")
	}
}

func TestAuthExtensionEnsureSessionOAuthTokens_UsesProvisionedUserIDLookup(t *testing.T) {
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
	users := &testUserService{userID: "user-42"}
	ext := &authExtension{
		cfg:        &Config{OAuth: &OAuth{Name: "oauth", Mode: "bff"}},
		sessions:   NewManager(0, nil),
		tokenStore: store,
		users:      users,
	}
	sess := &Session{
		ID:        "sess-1",
		Username:  "ppoudyal",
		Subject:   "agently_scheduler",
		CreatedAt: time.Now(),
	}

	ok := ext.ensureSessionOAuthTokens(context.Background(), sess)
	if !ok {
		t.Fatalf("ensureSessionOAuthTokens() = false, want true")
	}
	if store.getUser != "user-42" {
		t.Fatalf("token lookup user = %q, want %q", store.getUser, "user-42")
	}
	if sess.Tokens == nil || sess.Tokens.AccessToken != "access" {
		t.Fatalf("expected session tokens to be rehydrated")
	}
}
