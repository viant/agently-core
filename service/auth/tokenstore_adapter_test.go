package auth

import (
	"context"
	"testing"
	"time"

	token "github.com/viant/agently-core/internal/auth/token"
)

type canonicalStoreStub struct {
	getUsername string
	putUsername string
	delUsername string
	leaseUser   string
	releaseUser string
	casUser     string
	token       *OAuthToken
}

func (s *canonicalStoreStub) Get(_ context.Context, username, _ string) (*OAuthToken, error) {
	s.getUsername = username
	return s.token, nil
}

func (s *canonicalStoreStub) Put(_ context.Context, tok *OAuthToken) error {
	if tok != nil {
		s.putUsername = tok.Username
	}
	return nil
}

func (s *canonicalStoreStub) Delete(_ context.Context, username, _ string) error {
	s.delUsername = username
	return nil
}

func (s *canonicalStoreStub) TryAcquireRefreshLease(_ context.Context, username, _, _ string, _ time.Duration) (int64, bool, error) {
	s.leaseUser = username
	return 1, true, nil
}

func (s *canonicalStoreStub) ReleaseRefreshLease(_ context.Context, username, _, _ string) error {
	s.releaseUser = username
	return nil
}

func (s *canonicalStoreStub) CASPut(_ context.Context, tok *OAuthToken, _ int64, _ string) (bool, error) {
	if tok != nil {
		s.casUser = tok.Username
	}
	return true, nil
}

func TestCanonicalTokenStore_CanonicalizesOwnerAcrossOperations(t *testing.T) {
	raw := &canonicalStoreStub{
		token: &OAuthToken{
			Username:     "user-42",
			Provider:     "oauth",
			AccessToken:  "access",
			RefreshToken: "refresh",
			IDToken:      "id",
			ExpiresAt:    time.Now().Add(time.Hour),
		},
	}
	users := &testUserService{userBySubjectProvider: map[string]*User{
		"user-sub-123|oauth": {ID: "user-42", Username: "awitas"},
	}}
	store := &canonicalTokenStore{inner: raw, users: users}
	ctx := context.Background()

	if _, err := store.Get(ctx, "user-sub-123", "oauth"); err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if raw.getUsername != "user-42" {
		t.Fatalf("Get() username = %q, want canonical user ID %q", raw.getUsername, "user-42")
	}

	if err := store.Put(ctx, &OAuthToken{Username: "user-sub-123", Provider: "oauth"}); err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	if raw.putUsername != "user-42" {
		t.Fatalf("Put() username = %q, want canonical user ID %q", raw.putUsername, "user-42")
	}

	if err := store.Delete(ctx, "user-sub-123", "oauth"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if raw.delUsername != "user-42" {
		t.Fatalf("Delete() username = %q, want canonical user ID %q", raw.delUsername, "user-42")
	}

	if _, _, err := store.TryAcquireRefreshLease(ctx, "user-sub-123", "oauth", "owner-1", 30*time.Second); err != nil {
		t.Fatalf("TryAcquireRefreshLease() error = %v", err)
	}
	if raw.leaseUser != "user-42" {
		t.Fatalf("TryAcquireRefreshLease() username = %q, want canonical user ID %q", raw.leaseUser, "user-42")
	}

	if err := store.ReleaseRefreshLease(ctx, "user-sub-123", "oauth", "owner-1"); err != nil {
		t.Fatalf("ReleaseRefreshLease() error = %v", err)
	}
	if raw.releaseUser != "user-42" {
		t.Fatalf("ReleaseRefreshLease() username = %q, want canonical user ID %q", raw.releaseUser, "user-42")
	}

	if _, err := store.CASPut(ctx, &OAuthToken{Username: "user-sub-123", Provider: "oauth"}, 1, "owner-1"); err != nil {
		t.Fatalf("CASPut() error = %v", err)
	}
	if raw.casUser != "user-42" {
		t.Fatalf("CASPut() username = %q, want canonical user ID %q", raw.casUser, "user-42")
	}
}

func TestTokenStoreAdapter_CanonicalizesInternalTokenStoreBridge(t *testing.T) {
	raw := &canonicalStoreStub{
		token: &OAuthToken{
			Username:     "user-42",
			Provider:     "oauth",
			AccessToken:  "access",
			RefreshToken: "refresh",
			IDToken:      "id",
			ExpiresAt:    time.Now().Add(time.Hour),
		},
	}
	users := &testUserService{userBySubjectProvider: map[string]*User{
		"user-sub-123|oauth": {ID: "user-42", Username: "awitas"},
	}}
	store := NewTokenStoreAdapter(raw, users)
	ctx := context.Background()

	if _, err := store.Get(ctx, "user-sub-123", "oauth"); err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if raw.getUsername != "user-42" {
		t.Fatalf("Get() username = %q, want canonical user ID %q", raw.getUsername, "user-42")
	}

	if err := store.Put(ctx, &token.OAuthToken{Username: "user-sub-123", Provider: "oauth"}); err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	if raw.putUsername != "user-42" {
		t.Fatalf("Put() username = %q, want canonical user ID %q", raw.putUsername, "user-42")
	}
}
