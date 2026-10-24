package auth

import (
	"context"
	scyauth "github.com/viant/scy/auth"
	"golang.org/x/oauth2"
	"strings"
	"testing"
	"time"
)

func ownerFixture() runtimeAuthUser {
	return runtimeAuthUser{Subject: "verified-subject", Provider: "verified-provider", CanonicalUserID: "canonical-account", ValidUntil: time.Now().Add(time.Minute), Tokens: &scyauth.Token{Token: oauth2.Token{AccessToken: "PRIVATE_ACCESS", Expiry: time.Now().Add(time.Minute)}, IDToken: "PRIVATE_ID"}}
}
func TestLocalStateOwnerRequiresPrivateVerifiedIngress(t *testing.T) {
	user := ownerFixture()
	ctx := InjectTokens(InjectUser(context.Background(), user.Subject), user.Tokens)
	if owner, err := LocalStateOwner(ctx); err != nil || owner != nil {
		t.Fatal("public injection created owner")
	}
	first, err := LocalStateOwner(withRuntimeAuthUser(context.Background(), &user))
	if err != nil || first == nil || len(first.Binding) != 64 || strings.Contains(first.Binding, "PRIVATE") {
		t.Fatal("verified owner unavailable")
	}
	for _, field := range []string{"subject", "account", "provider", "credential"} {
		next := ownerFixture()
		switch field {
		case "subject":
			next.Subject = "another"
		case "account":
			next.CanonicalUserID = "another"
		case "provider":
			next.Provider = "another"
		case "credential":
			next.Tokens.IDToken = "ROTATED"
		}
		owner, _ := LocalStateOwner(withRuntimeAuthUser(context.Background(), &next))
		if owner == nil || owner.Binding == first.Binding {
			t.Fatalf("%s change retained owner", field)
		}
	}
	user.ValidUntil = time.Now().Add(2 * time.Minute)
	renewed, _ := LocalStateOwner(withRuntimeAuthUser(context.Background(), &user))
	if renewed.Binding != first.Binding {
		t.Fatal("lease renewal changed binding")
	}
	user.ValidUntil = time.Now().Add(-time.Second)
	if owner, _ := LocalStateOwner(withRuntimeAuthUser(context.Background(), &user)); owner != nil {
		t.Fatal("expired ingress created owner")
	}
	user = ownerFixture()
	user.Tokens = nil
	if owner, _ := LocalStateOwner(withRuntimeAuthUser(context.Background(), &user)); owner != nil {
		t.Fatal("missing credential revision created owner")
	}
}
func TestLocalStateOwnerSessionDeadlineClampsCredentialLease(t *testing.T) {
	s := &Session{ExpiresAt: time.Now().Add(time.Hour)}
	tokens := ownerFixture().Tokens
	if got := sessionOwnerDeadline(s, tokens); !got.Equal(tokens.Expiry) {
		t.Fatal("credential deadline not clamped")
	}
	s.ExpiresAt = time.Now().Add(time.Second)
	if got := sessionOwnerDeadline(s, tokens); !got.Equal(s.ExpiresAt) {
		t.Fatal("session deadline not clamped")
	}
}
