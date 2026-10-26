package auth

import (
	"context"
	"testing"
)

type conditionalAdapterStore struct {
	canonicalStoreStub
	clearUser, clearProvider, clearOwner string
	clearVersion                         int64
}

func (s *conditionalAdapterStore) CASClear(_ context.Context, user, provider string, version int64, owner string) (bool, error) {
	s.clearUser, s.clearProvider, s.clearVersion, s.clearOwner = user, provider, version, owner
	return true, nil
}
func TestRefreshConditionalClearAdapterPreservesCanonicalOwnerAndFence(t *testing.T) {
	raw := &conditionalAdapterStore{}
	users := &testUserService{userBySubjectProvider: map[string]*User{"subject|oauth": {ID: "canonical-user"}}}
	canonical := &canonicalTokenStore{inner: raw, users: users}
	adapter := &tokenStoreAdapter{inner: canonical}
	cleared, err := adapter.CASClear(context.Background(), "subject", "oauth", 17, "attempt-owner")
	if err != nil || !cleared || raw.clearUser != "canonical-user" || raw.clearProvider != "oauth" || raw.clearVersion != 17 || raw.clearOwner != "attempt-owner" {
		t.Fatal("conditional clear scope changed")
	}
	unsupported := &canonicalTokenStore{inner: &canonicalStoreStub{}, users: users}
	cleared, err = unsupported.CASClear(context.Background(), "subject", "oauth", 17, "attempt-owner")
	if err != nil || cleared {
		t.Fatal("missing optional support fabricated successful clear")
	}
}
