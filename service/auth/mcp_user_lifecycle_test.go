package auth

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	iauth "github.com/viant/agently-core/internal/auth"
	oauthwrite "github.com/viant/agently-core/pkg/agently/user/oauth/write"
)

// fakeUserLookup implements UserByIDLookup for lifecycle tests.
type fakeUserLookup struct {
	users map[string]*User
	err   error
}

func (f *fakeUserLookup) GetByID(_ context.Context, id string) (*User, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.users[strings.TrimSpace(id)], nil
}

func TestDelegatedResolver_DisabledUserFailsClosed(t *testing.T) {
	store := newFakeDelegatedStore()
	storageKey := DelegatedProviderStorageKey("test-ns", "adelphic-dev6")
	store.seed(&OAuthToken{
		Username:     "user-1",
		Provider:     storageKey,
		AccessToken:  "access",
		RefreshToken: "refresh",
		ExpiresAt:    time.Now().Add(time.Hour),
		ProviderRef:  "adelphic-dev6",
		Issuer:       "https://idp-dev6.example.com",
		Resource:     "https://mcp6.example.com/mcp",
		Scopes:       []string{"plan:create", "plan:edit", "plan:read"},
	})
	resolver := newTestResolver(t, store, dev6ProviderDoc(false))
	resolver.users = &fakeUserLookup{users: map[string]*User{"user-1": {ID: "user-1", Disabled: true}}}

	ctx := iauth.WithCanonicalUserID(context.Background(), "user-1")
	ctx = iauth.WithUserInfo(ctx, &iauth.UserInfo{Subject: "subject-1"})
	_, err := resolver.Resolve(ctx, dev6Requirement())
	if err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("Resolve() for disabled user = %v, want disabled failure", err)
	}

	// The background watcher also refuses without mutating the row.
	stored, _ := store.GetExact(context.Background(), "user-1", storageKey)
	if err := resolver.RefreshStoredDelegatedToken(context.Background(), stored); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("RefreshStoredDelegatedToken() for disabled user = %v, want disabled failure", err)
	}
	if after, _ := store.GetExact(context.Background(), "user-1", storageKey); after == nil || after.AccessToken != "access" {
		t.Fatalf("disabled-user refresh mutated the stored row: %+v", after)
	}

	// Deleted users (no row) also fail closed.
	resolver.users = &fakeUserLookup{users: map[string]*User{}}
	if _, err := resolver.Resolve(ctx, dev6Requirement()); err == nil {
		t.Fatalf("Resolve() for deleted user succeeded")
	}
	// Lookup failures fail closed rather than serving a credential.
	resolver.users = &fakeUserLookup{err: fmt.Errorf("lookup down")}
	if _, err := resolver.Resolve(ctx, dev6Requirement()); err == nil {
		t.Fatalf("Resolve() with failing lookup succeeded")
	}
}

func TestCleanupDelegatedCredentials(t *testing.T) {
	ctx := context.Background()
	dao := newMCPLinkTestDAO(t)
	if _, err := oauthwrite.DefineComponent(ctx, dao); err != nil {
		t.Fatalf("oauth write DefineComponent() error = %v", err)
	}
	cfg := &Config{Enabled: true, TokenEncryptionKey: "cleanup-test-key"}
	delegated := NewDelegatedMCPAuth(cfg, dao)
	if delegated == nil {
		t.Fatalf("NewDelegatedMCPAuth() = nil")
	}
	users := NewDatlyUserService(dao)
	canonical, err := users.UpsertWithProvider(ctx, "cleanup-user", "Cleanup", "cleanup@example.test", "oauth", "cleanup-subject")
	if err != nil || canonical == "" {
		t.Fatalf("UpsertWithProvider() = %q, %v", canonical, err)
	}

	storageKey := DelegatedProviderStorageKey("default", "cleanup-provider")
	if err := delegated.resolver.store.Put(ctx, &OAuthToken{
		Username:     canonical,
		Provider:     storageKey,
		AccessToken:  "access",
		RefreshToken: "refresh",
		ExpiresAt:    time.Now().Add(time.Hour),
		ProviderRef:  "cleanup-provider",
		Issuer:       "https://idp.test/",
		Resource:     "https://mcp.test/mcp",
	}); err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	// A workspace row must never be touched by delegated cleanup.
	if err := delegated.resolver.store.Put(ctx, &OAuthToken{
		Username:    canonical,
		Provider:    "oauth",
		AccessToken: "workspace-access",
		ExpiresAt:   time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("workspace Put() error = %v", err)
	}

	lister, ok := delegated.resolver.store.(DelegatedTokenLister)
	if !ok {
		t.Fatalf("token store does not implement DelegatedTokenLister")
	}
	rows, err := lister.ListDelegated(ctx, canonical)
	if err != nil || len(rows) != 1 {
		t.Fatalf("ListDelegated() = %d rows, %v; want 1 delegated row", len(rows), err)
	}

	if err := delegated.CleanupDelegatedCredentials(ctx, canonical); err != nil {
		t.Fatalf("CleanupDelegatedCredentials() error = %v", err)
	}
	if stored, _ := delegated.resolver.store.GetExact(ctx, canonical, storageKey); stored != nil {
		t.Fatalf("delegated row survived lifecycle cleanup: %+v", stored)
	}
	if workspace, _ := delegated.resolver.store.GetExact(ctx, canonical, "oauth"); workspace == nil || workspace.AccessToken != "workspace-access" {
		t.Fatalf("workspace token was affected by delegated cleanup: %+v", workspace)
	}
}
