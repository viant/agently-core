package auth

import (
	"context"
	"testing"

	scyauth "github.com/viant/scy/auth"
	"golang.org/x/oauth2"
)

func TestCanonicalUserIDRoundTrip(t *testing.T) {
	ctx := context.Background()
	if got := CanonicalUserID(ctx); got != "" {
		t.Fatalf("expected empty canonical user, got %q", got)
	}
	ctx = WithCanonicalUserID(ctx, "  user-123  ")
	if got := CanonicalUserID(ctx); got != "user-123" {
		t.Fatalf("expected trimmed canonical user, got %q", got)
	}
	// Empty values never install a key.
	if got := CanonicalUserID(WithCanonicalUserID(context.Background(), "   ")); got != "" {
		t.Fatalf("expected empty canonical user for blank input, got %q", got)
	}
}

func TestCanonicalUserIDDoesNotReplaceEffectiveUserID(t *testing.T) {
	ctx := WithUserInfo(context.Background(), &UserInfo{Subject: "subject@corp"})
	ctx = WithCanonicalUserID(ctx, "canonical-uuid")
	if got := EffectiveUserID(ctx); got != "subject@corp" {
		t.Fatalf("EffectiveUserID must stay on the workspace subject, got %q", got)
	}
	if got := CanonicalUserID(ctx); got != "canonical-uuid" {
		t.Fatalf("CanonicalUserID = %q", got)
	}
}

func TestWithoutTokensPreservesCanonicalAndIdentity(t *testing.T) {
	ctx := WithUserInfo(context.Background(), &UserInfo{Subject: "subject@corp"})
	ctx = WithCanonicalUserID(ctx, "canonical-uuid")
	ctx = WithProvider(ctx, "workspace-oauth")
	ctx = WithTokens(ctx, &scyauth.Token{Token: oauth2.Token{AccessToken: "secret"}})
	masked := WithoutTokens(ctx)
	if TokensFromContext(masked) != nil {
		t.Fatalf("expected tokens masked")
	}
	if got := CanonicalUserID(masked); got != "canonical-uuid" {
		t.Fatalf("canonical user must survive token masking, got %q", got)
	}
	if got := Provider(masked); got != "workspace-oauth" {
		t.Fatalf("workspace provider must survive token masking, got %q", got)
	}
	if got := EffectiveUserID(masked); got != "subject@corp" {
		t.Fatalf("effective user must survive token masking, got %q", got)
	}
}
