package sdk

import (
	"context"
	"testing"

	authctx "github.com/viant/agently-core/internal/auth"
)

// TestPrincipalDataOpts_EmptyWhenNoUser guards the scheduler / background /
// local-mode path: without an authenticated caller on the ctx, the helper
// must return nil so data-layer calls skip the ownership check exactly as
// they did before the turn-level ownership fix landed.
func TestPrincipalDataOpts_EmptyWhenNoUser(t *testing.T) {
	opts := principalDataOpts(context.Background())
	if len(opts) != 0 {
		t.Fatalf("expected empty opts without user, got %d", len(opts))
	}
}

// TestPrincipalDataOpts_SetsPrincipalForAuthenticatedCaller verifies the new
// wiring: an HTTP request that carries an authenticated subject yields a
// data.Option that authorizeConversationID can use to enforce ownership.
func TestPrincipalDataOpts_SetsPrincipalForAuthenticatedCaller(t *testing.T) {
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "user-42"})
	opts := principalDataOpts(ctx)
	if len(opts) != 1 {
		t.Fatalf("expected 1 data option for authenticated caller, got %d", len(opts))
	}
}

// TestPrincipalDataOpts_IgnoresBlankSubject covers a minor edge case — a
// UserInfo with only whitespace in Subject should behave the same as an
// unauthenticated caller so we don't accidentally gate on a meaningless
// principal.
func TestPrincipalDataOpts_IgnoresBlankSubject(t *testing.T) {
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "   "})
	opts := principalDataOpts(ctx)
	if len(opts) != 0 {
		t.Fatalf("expected empty opts for blank subject, got %d", len(opts))
	}
}
