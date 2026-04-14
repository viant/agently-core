package auth

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/viant/agently-core/app/store/data"
)

const (
	testOAuthSubject  = "oauth_subject_test"
	testOAuthUsername = "oauth_user_test"
	testOAuthEmail    = "oauth_user_test@example.test"
)

// What it protects:
// - persistOAuthToken must store user_oauth_token.user_id as canonical users.id, not oauth subject
// - the persisted token must be readable by canonical user id
// - the token must not be readable by subject
func TestAuthExtensionPersistOAuthToken_StoresUnderCanonicalUserID_WithDatly(t *testing.T) {
	ctx := context.Background()
	dao, err := data.NewDatlyInMemory(ctx)
	if err != nil {
		t.Fatalf("NewDatlyInMemory() error = %v", err)
	}

	users := NewDatlyUserService(dao)
	if users == nil {
		t.Fatalf("NewDatlyUserService() = nil")
	}
	store := NewTokenStoreDAO(dao, "oauth-persist-datly-test")
	if store == nil {
		t.Fatalf("NewTokenStoreDAO() = nil")
	}

	ext := &authExtension{
		cfg:        &Config{OAuth: &OAuth{Name: "oauth", Mode: "bff"}},
		sessions:   NewManager(0, nil),
		tokenStore: store,
		users:      users,
	}

	subject := testOAuthSubject
	username := testOAuthUsername
	ext.persistOAuthToken(ctx, "oauth_callback", username, testOAuthEmail, subject, "oauth", "access", "id", "refresh", time.Now().Add(time.Hour))

	user, err := users.GetBySubjectAndProvider(ctx, subject, "oauth")
	if err != nil {
		t.Fatalf("GetBySubjectAndProvider() error = %v", err)
	}
	if user == nil {
		t.Fatalf("GetBySubjectAndProvider() = nil")
	}
	if user.ID == "" {
		t.Fatalf("user.ID was empty")
	}

	db, err := store.db()
	if err != nil {
		t.Fatalf("store.db() error = %v", err)
	}

	var tokenUserID string
	if err := db.QueryRowContext(ctx, `SELECT user_id FROM user_oauth_token WHERE provider = ?`, "oauth").Scan(&tokenUserID); err != nil {
		t.Fatalf("QueryRowContext(user_oauth_token) error = %v", err)
	}
	if tokenUserID != user.ID {
		t.Fatalf("user_oauth_token.user_id = %q, want canonical users.id %q", tokenUserID, user.ID)
	}
	if tokenUserID == subject {
		t.Fatalf("user_oauth_token.user_id = %q, want canonical users.id instead of subject", tokenUserID)
	}

	if token, err := store.Get(ctx, user.ID, "oauth"); err != nil {
		t.Fatalf("store.Get(canonical ID) error = %v", err)
	} else if token == nil || token.AccessToken != "access" || token.IDToken != "id" || token.RefreshToken != "refresh" {
		t.Fatalf("store.Get(canonical ID) returned %+v, want persisted token payload", token)
	}

	if token, err := store.Get(ctx, subject, "oauth"); err != nil {
		t.Fatalf("store.Get(subject) error = %v", err)
	} else if token != nil {
		t.Fatalf("store.Get(subject) = %+v, want nil because tokens must be keyed by canonical users.id", token)
	}
}

// What it protects:
// - ensureSessionOAuthTokens must resolve canonical users.id before loading user_oauth_token
// - session rehydration must work with a real Datly-backed users table and token store
// - no token row should ever be created under subject instead of users.id
func TestAuthExtensionEnsureSessionOAuthTokens_RehydratesUsingCanonicalUserID_WithDatly(t *testing.T) {
	ctx := context.Background()
	dao, err := data.NewDatlyInMemory(ctx)
	if err != nil {
		t.Fatalf("NewDatlyInMemory() error = %v", err)
	}

	users := NewDatlyUserService(dao)
	if users == nil {
		t.Fatalf("NewDatlyUserService() = nil")
	}
	store := NewTokenStoreDAO(dao, "oauth-ensure-datly-test")
	if store == nil {
		t.Fatalf("NewTokenStoreDAO() = nil")
	}

	ext := &authExtension{
		cfg:        &Config{OAuth: &OAuth{Name: "oauth", Mode: "bff"}},
		sessions:   NewManager(0, nil),
		tokenStore: store,
		users:      users,
	}

	subject := testOAuthSubject
	username := testOAuthUsername
	ext.persistOAuthToken(ctx, "oauth_callback", username, testOAuthEmail, subject, "oauth", "access", "id", "refresh", time.Now().Add(time.Hour))

	sess := &Session{
		ID:        "sess-1",
		Username:  username,
		Subject:   subject,
		Provider:  "oauth",
		CreatedAt: time.Now(),
	}

	if ok := ext.ensureSessionOAuthTokens(ctx, sess); !ok {
		t.Fatalf("ensureSessionOAuthTokens() = false, want true")
	}
	if sess.Tokens == nil {
		t.Fatalf("sess.Tokens = nil")
	}
	if sess.Tokens.AccessToken != "access" || sess.Tokens.IDToken != "id" || sess.Tokens.RefreshToken != "refresh" {
		t.Fatalf("rehydrated tokens = %+v, want persisted oauth token payload", sess.Tokens)
	}

	user, err := users.GetBySubjectAndProvider(ctx, subject, "oauth")
	if err != nil {
		t.Fatalf("GetBySubjectAndProvider() error = %v", err)
	}
	if user == nil || user.ID == "" {
		t.Fatalf("GetBySubjectAndProvider() returned empty user")
	}

	db, err := store.db()
	if err != nil {
		t.Fatalf("store.db() error = %v", err)
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(1) FROM user_oauth_token WHERE user_id = ? AND provider = ?`, user.ID, "oauth").Scan(&count); err != nil {
		t.Fatalf("QueryRowContext(count canonical token row) error = %v", err)
	}
	if count != 1 {
		t.Fatalf("canonical token row count = %d, want 1", count)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(1) FROM user_oauth_token WHERE user_id = ? AND provider = ?`, subject, "oauth").Scan(&count); err != nil && err != sql.ErrNoRows {
		t.Fatalf("QueryRowContext(count subject token row) error = %v", err)
	}
	if count != 0 {
		t.Fatalf("subject token row count = %d, want 0", count)
	}
}
