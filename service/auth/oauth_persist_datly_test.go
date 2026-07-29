package auth

import (
	"context"
	"database/sql"
	"strings"
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

func TestTokenStoreDAODelete_ClearsCredentialAndResetsAuditLeaseState(t *testing.T) {
	ctx := context.Background()
	dao, err := data.NewDatlyInMemory(ctx)
	if err != nil {
		t.Fatalf("NewDatlyInMemory() error = %v", err)
	}
	users := NewDatlyUserService(dao)
	userID, err := users.UpsertWithProvider(ctx, "delete-user", "delete-user", "delete@example.test", "oauth", "delete-subject")
	if err != nil {
		t.Fatalf("UpsertWithProvider() error = %v", err)
	}
	store := NewTokenStoreDAO(dao, "oauth-delete-audit-test")
	if err := store.Put(ctx, &OAuthToken{
		Username:     userID,
		Provider:     "oauth",
		AccessToken:  "access",
		RefreshToken: "refresh",
		ExpiresAt:    time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	db, err := store.db()
	if err != nil {
		t.Fatalf("store.db() error = %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE user_oauth_token
SET lease_owner = 'worker', lease_until = DATETIME('now', '+1 hour'),
    refresh_status = 'refreshing', updated_at = DATETIME('2000-01-01')
WHERE user_id = ? AND provider = ?`, userID, "oauth"); err != nil {
		t.Fatalf("prepare audit state: %v", err)
	}
	var versionBefore int64
	if err := db.QueryRowContext(ctx,
		`SELECT version FROM user_oauth_token WHERE user_id = ? AND provider = ?`,
		userID, "oauth",
	).Scan(&versionBefore); err != nil {
		t.Fatalf("read version before delete: %v", err)
	}

	if err := store.Delete(ctx, userID, "oauth"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	var (
		encToken      string
		versionAfter  int64
		leaseOwner    string
		leaseUntil    string
		refreshStatus string
		updatedAt     string
	)
	if err := db.QueryRowContext(ctx, `SELECT enc_token, version,
       COALESCE(lease_owner, ''), COALESCE(CAST(lease_until AS TEXT), ''),
       refresh_status, CAST(updated_at AS TEXT)
FROM user_oauth_token WHERE user_id = ? AND provider = ?`, userID, "oauth").
		Scan(&encToken, &versionAfter, &leaseOwner, &leaseUntil, &refreshStatus, &updatedAt); err != nil {
		t.Fatalf("read row after delete: %v", err)
	}
	if encToken != "" {
		t.Fatalf("enc_token = %q, want empty", encToken)
	}
	if versionAfter != versionBefore+1 {
		t.Fatalf("version = %d, want %d", versionAfter, versionBefore+1)
	}
	if leaseOwner != "" || leaseUntil != "" || refreshStatus != "idle" {
		t.Fatalf("lease state = owner %q until %q status %q", leaseOwner, leaseUntil, refreshStatus)
	}
	if strings.HasPrefix(updatedAt, "2000-01-01") {
		t.Fatalf("updated_at was not advanced: %q", updatedAt)
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
