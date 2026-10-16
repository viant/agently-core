package auth

import (
	"context"
	"testing"
	"time"

	"github.com/viant/authz"
	"github.com/viant/authz/gating"
	scyauth "github.com/viant/scy/auth"
)

type factsSource func(context.Context) (authz.Facts, error)

func (f factsSource) Resolve(ctx context.Context) (authz.Facts, error) { return f(ctx) }

func TestIDTokenFactProviderDoesNotUseAccessTokenFallback(t *testing.T) {
	called := false
	provider := IDTokenFactProvider{Source: factsSource(func(context.Context) (authz.Facts, error) {
		called = true
		return authz.Facts{Subject: "verified"}, nil
	})}
	tokens := &scyauth.Token{}
	tokens.Token.AccessToken = "access-only"
	ctx := InjectTokens(context.Background(), tokens)
	if _, err := provider.Resolve(ctx); err == nil || called {
		t.Fatalf("access token was used for user-info authority: err=%v called=%v", err, called)
	}
	tokens.IDToken = "id-token"
	ctx = InjectTokens(context.Background(), tokens)
	if facts, err := provider.Resolve(ctx); err != nil || facts.Subject != "verified" || !called {
		t.Fatalf("ID token was not passed to verified provider: %+v %v", facts, err)
	}
}

type principalSource struct{ calls int }

func (s *principalSource) Resolve(context.Context) (authz.Facts, error) {
	s.calls++
	return authz.Facts{Subject: "verified"}, nil
}

func (s *principalSource) ResolvePrincipal(context.Context) (gating.Principal, error) {
	s.calls++
	return gating.Principal{Facts: authz.Facts{Subject: "verified"}, AccountID: "21"}, nil
}

func TestIDTokenIdentityProviderUsesOneVerifiedSourceForACLAndGate(t *testing.T) {
	source := &principalSource{}
	provider := IDTokenIdentityProvider{Source: source}
	tokens := &scyauth.Token{}
	tokens.Token.AccessToken = "access-only"
	ctx := InjectTokens(context.Background(), tokens)
	if _, err := provider.Resolve(ctx); err == nil {
		t.Fatal("ACL accepted access token without ID token")
	}
	if _, err := provider.ResolvePrincipal(ctx); err == nil || source.calls != 0 {
		t.Fatalf("gate accepted access token or invoked source: %v calls=%d", err, source.calls)
	}
	tokens.IDToken = "id-token"
	ctx = InjectTokens(context.Background(), tokens)
	facts, err := provider.Resolve(ctx)
	if err != nil || facts.Subject != "verified" {
		t.Fatalf("ACL facts=%+v err=%v", facts, err)
	}
	principal, err := provider.ResolvePrincipal(ctx)
	if err != nil || principal.AccountID != "21" || source.calls != 2 {
		t.Fatalf("gate principal=%+v err=%v calls=%d", principal, err, source.calls)
	}
}

type accountSourceFunc func(context.Context, authz.Facts) (string, error)

func (f accountSourceFunc) Account(ctx context.Context, facts authz.Facts) (string, error) {
	return f(ctx, facts)
}

func TestIDTokenAccountResolverRequiresIDToken(t *testing.T) {
	called := false
	resolve := IDTokenAccountResolver(accountSourceFunc(func(context.Context, authz.Facts) (string, error) { called = true; return "account", nil }))
	tokens := &scyauth.Token{}
	tokens.Token.AccessToken = "access-only"
	if _, err := resolve(InjectTokens(context.Background(), tokens), authz.Facts{Subject: "alice"}); err == nil || called {
		t.Fatalf("account resolver used access token: %v called=%v", err, called)
	}
	tokens.IDToken = "id-token"
	if account, err := resolve(InjectTokens(context.Background(), tokens), authz.Facts{Subject: "alice"}); err != nil || account != "account" || !called {
		t.Fatalf("account=%q called=%v err=%v", account, called, err)
	}
}

type authorityRevisionSourceFunc func(context.Context, authz.Facts, string) (string, time.Time, error)

func (f authorityRevisionSourceFunc) AuthorityRevision(ctx context.Context, facts authz.Facts, accountID string) (string, time.Time, error) {
	return f(ctx, facts, accountID)
}

func TestIDTokenAuthorityRevisionRequiresIDToken(t *testing.T) {
	called := false
	deadline := time.Now().Add(time.Minute)
	resolve := IDTokenAuthorityRevisionResolver(authorityRevisionSourceFunc(func(_ context.Context, facts authz.Facts, accountID string) (string, time.Time, error) {
		called = true
		if facts.Subject != "alice" || accountID != "account" {
			t.Fatal("authority binding changed")
		}
		return "revision", deadline, nil
	}))
	tokens := &scyauth.Token{}
	tokens.Token.AccessToken = "access-only"
	if _, _, err := resolve(InjectTokens(context.Background(), tokens), authz.Facts{Subject: "alice"}, "account"); err == nil || called {
		t.Fatalf("authority revision used access token: %v called=%v", err, called)
	}
	tokens.IDToken = "id-token"
	if revision, lease, err := resolve(InjectTokens(context.Background(), tokens), authz.Facts{Subject: "alice"}, "account"); err != nil || revision != "revision" || !lease.Equal(deadline) || !called {
		t.Fatalf("authority revision=%q lease=%v called=%v err=%v", revision, lease, called, err)
	}
}
