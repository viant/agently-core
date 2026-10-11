package auth

import (
	"context"
	internalAuth "github.com/viant/agently-core/internal/auth"
	scyauth "github.com/viant/scy/auth"
	"testing"
)

func TestCanonicalIngressProjectionsReadOnly(t *testing.T) {
	unverified := InjectTokens(InjectUser(context.Background(), "unverified-subject"), &scyauth.Token{IDToken: "unverified-text"})
	for _, ctx := range []context.Context{nil, context.Background(), unverified, context.WithValue(context.Background(), "canonicalUserID", "forged")} {
		if CanonicalUserID(ctx) != "" || VerifiedProvider(ctx) != "" {
			t.Fatal("unverified identity inferred canonical mapping/provider")
		}
	}
	verified := internalAuth.WithCanonicalUserID(internalAuth.WithProvider(context.Background(), "configured-idp"), "canonical-user")
	if CanonicalUserID(verified) != "canonical-user" || VerifiedProvider(verified) != "configured-idp" {
		t.Fatal("trusted ingress projection lost")
	}
	if EffectiveUserID(verified) != "" {
		t.Fatal("read-only canonical projection injected provider subject")
	}
}
