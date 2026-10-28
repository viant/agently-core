package auth

import (
	"context"
	"fmt"
	"time"

	iauth "github.com/viant/agently-core/internal/auth"
)

// VerifiedContextSnapshot retains an already verified ingress identity for a
// bounded continuation in this process. It is never serialized and cannot be
// constructed from public user/token context injection.
type VerifiedContextSnapshot struct {
	user      runtimeAuthUser
	canonical string
}

func CaptureVerifiedContext(ctx context.Context) (*VerifiedContextSnapshot, error) {
	owner, err := LocalStateOwner(ctx)
	if err != nil || owner == nil {
		return nil, err
	}
	user := ctx.Value(runtimeAuthContextKey{}).(runtimeAuthUser)
	if user.Tokens != nil {
		tokens := *user.Tokens
		user.Tokens = &tokens
	}
	return &VerifiedContextSnapshot{user: user, canonical: iauth.CanonicalUserID(ctx)}, nil
}

func (s *VerifiedContextSnapshot) ValidUntil() time.Time {
	if s == nil {
		return time.Time{}
	}
	return s.user.ValidUntil
}

// Restore preserves the continuation's cancellation and storage context. The
// original verified deadline is not renewed, and existing identity cannot be
// replaced by another principal.
func (s *VerifiedContextSnapshot) Restore(ctx context.Context) (context.Context, error) {
	if s == nil || ctx == nil || ctx.Err() != nil || !s.user.ValidUntil.After(time.Now()) {
		return nil, fmt.Errorf("verified execution context expired or unavailable")
	}
	if actor := EffectiveUserID(ctx); actor != "" && actor != s.user.effectiveUserID() {
		return nil, fmt.Errorf("verified execution context identity mismatch")
	}
	if canonical := iauth.CanonicalUserID(ctx); canonical != "" && canonical != s.canonical {
		return nil, fmt.Errorf("verified execution context identity mismatch")
	}
	return withRuntimeAuthUser(ctx, &s.user), nil
}
