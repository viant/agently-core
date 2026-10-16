package auth

import (
	"context"
	scyauth "github.com/viant/scy/auth"
	"golang.org/x/oauth2"
	"testing"
)

func TestTokenBundleDoesNotRewriteOriginalIngressValues(t *testing.T) {
	original := WithIDToken(WithBearer(context.Background(), "ingress-access"), "ingress-id")
	updated := WithTokens(original, &scyauth.Token{Token: oauth2.Token{AccessToken: "refreshed-access"}, IDToken: "refreshed-id"})
	if Bearer(updated) != "ingress-access" || IDToken(updated) != "ingress-id" {
		t.Fatal("storing a token bundle changed original ingress values")
	}
	if MCPAuthToken(updated, false) != "refreshed-access" || MCPAuthToken(updated, true) != "refreshed-id" {
		t.Fatal("outbound selection did not use the current bundle")
	}
	if Bearer(original) != "ingress-access" || IDToken(original) != "ingress-id" {
		t.Fatal("parent context changed")
	}
}
