package auth

import (
	"context"
	"fmt"
	"sync"
	"testing"
)

type fakeUserService struct {
	mu               sync.Mutex
	bySubjectAndProv map[string]*User
	byUsername       map[string]*User
	lookups          int
	err              error
}

func (f *fakeUserService) key(subject, provider string) string { return provider + "|" + subject }

func (f *fakeUserService) GetByUsername(ctx context.Context, username string) (*User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	return f.byUsername[username], nil
}

func (f *fakeUserService) GetBySubjectAndProvider(ctx context.Context, subject, provider string) (*User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lookups++
	if f.err != nil {
		return nil, f.err
	}
	return f.bySubjectAndProv[f.key(subject, provider)], nil
}

func (f *fakeUserService) Upsert(ctx context.Context, user *User) error { return nil }

func (f *fakeUserService) UpsertWithProvider(ctx context.Context, username, displayName, email, provider, subject string) (string, error) {
	return "", fmt.Errorf("canonical resolution must never upsert users")
}

func (f *fakeUserService) UpdateHashIPByID(ctx context.Context, id, hash string) error { return nil }

func (f *fakeUserService) UpdatePreferences(ctx context.Context, username string, patch *PreferencesPatch) error {
	return nil
}

func TestResolveCanonicalWorkspaceUser(t *testing.T) {
	users := &fakeUserService{bySubjectAndProv: map[string]*User{
		"corp-idp|subject@corp": {ID: "uuid-1"},
	}}
	resolver := NewCanonicalUserResolver(users)
	identity := VerifiedWorkspaceIdentity{Provider: "corp-idp", Subject: "subject@corp"}

	id, err := resolver.ResolveCanonicalWorkspaceUser(context.Background(), identity)
	if err != nil || id != "uuid-1" {
		t.Fatalf("resolve = (%q, %v), want (uuid-1, nil)", id, err)
	}
	// Second call is served from cache (30s hard TTL).
	if _, err := resolver.ResolveCanonicalWorkspaceUser(context.Background(), identity); err != nil {
		t.Fatalf("cached resolve: %v", err)
	}
	if users.lookups != 1 {
		t.Fatalf("expected 1 backend lookup, got %d", users.lookups)
	}
	// Invalidate forces a fresh lookup.
	resolver.Invalidate()
	if _, err := resolver.ResolveCanonicalWorkspaceUser(context.Background(), identity); err != nil {
		t.Fatalf("post-invalidate resolve: %v", err)
	}
	if users.lookups != 2 {
		t.Fatalf("expected 2 backend lookups after invalidation, got %d", users.lookups)
	}
}

func TestResolveCanonicalWorkspaceUserFailsClosed(t *testing.T) {
	resolver := NewCanonicalUserResolver(&fakeUserService{bySubjectAndProv: map[string]*User{}})
	// Unknown identity: no canonical user, no creation, hard error.
	if _, err := resolver.ResolveCanonicalWorkspaceUser(context.Background(), VerifiedWorkspaceIdentity{Provider: "corp-idp", Subject: "ghost"}); err == nil {
		t.Fatalf("unknown identity must fail closed")
	}
	// Missing provider or subject: rejected before lookup.
	if _, err := resolver.ResolveCanonicalWorkspaceUser(context.Background(), VerifiedWorkspaceIdentity{Subject: "s"}); err == nil {
		t.Fatalf("missing provider must fail closed")
	}
	if _, err := resolver.ResolveCanonicalWorkspaceUser(context.Background(), VerifiedWorkspaceIdentity{Provider: "p"}); err == nil {
		t.Fatalf("missing subject must fail closed")
	}
	// Lookup error propagates instead of guessing.
	failing := NewCanonicalUserResolver(&fakeUserService{err: fmt.Errorf("db down")})
	if _, err := failing.ResolveCanonicalWorkspaceUser(context.Background(), VerifiedWorkspaceIdentity{Provider: "p", Subject: "s"}); err == nil {
		t.Fatalf("backend failure must fail closed")
	}
}

// TestSessionAndBearerShareCanonicalResolver proves both entry paths resolve
// through the same resolver instance owned by the auth extension.
func TestSessionAndBearerShareCanonicalResolver(t *testing.T) {
	users := &fakeUserService{bySubjectAndProv: map[string]*User{
		"corp-idp|subject@corp": {ID: "uuid-1", Username: "subject", Email: "subject@corp"},
	}}
	cfg := &Config{Enabled: true, OAuth: &OAuth{Name: "corp-idp"}}
	ext := newAuthExtension(cfg, NewManager(0, nil), "", nil, users)
	runtime := &Runtime{cfg: cfg, ext: ext}

	// Session path.
	sess := &Session{Subject: "subject@corp", Provider: "corp-idp"}
	ext.canonicalizeSessionUser(context.Background(), sess)
	if sess.UserID != "uuid-1" {
		t.Fatalf("session canonicalization = %q, want uuid-1", sess.UserID)
	}

	// Bearer path uses the identical resolver (and its cache).
	users.mu.Lock()
	before := users.lookups
	users.mu.Unlock()
	got := runtime.resolveBearerCanonicalUserID(context.Background(), "subject@corp", "subject@corp")
	if got != "uuid-1" {
		t.Fatalf("bearer canonicalization = %q, want uuid-1", got)
	}
	users.mu.Lock()
	after := users.lookups
	users.mu.Unlock()
	if after != before {
		t.Fatalf("bearer path must reuse the shared resolver cache (lookups %d -> %d)", before, after)
	}

	// runtimeAuthUser propagation into context.
	ctx := withRuntimeAuthUser(context.Background(), &runtimeAuthUser{
		EffectiveUserID: "subject@corp",
		CanonicalUserID: "uuid-1",
		Subject:         "subject@corp",
		Provider:        "corp-idp",
	})
	if got := EffectiveUserID(ctx); got != "subject@corp" {
		t.Fatalf("EffectiveUserID = %q", got)
	}
}
