package auth

import (
	"context"
	"github.com/viant/forge/backend/types"
	scyauth "github.com/viant/scy/auth"
	"github.com/viant/scy/auth/jwt"
	"strings"
	"time"
)

// LocalStateOwner consumes only the private verified-ingress context. Public
// user/token injection and browser claims cannot create restoration scope.
func LocalStateOwner(ctx context.Context) (*types.LocalStateOwner, error) {
	if ctx == nil || ctx.Err() != nil {
		return nil, nil
	}
	user, ok := ctx.Value(runtimeAuthContextKey{}).(runtimeAuthUser)
	if !ok || strings.TrimSpace(user.Subject) == "" || strings.TrimSpace(user.Provider) == "" || !user.ValidUntil.After(time.Now()) {
		return nil, nil
	}
	if user.Tokens == nil || (strings.TrimSpace(user.Tokens.IDToken) == "" && strings.TrimSpace(user.Tokens.AccessToken) == "") {
		return nil, nil
	}
	binding := meSessionBinding(ctx, user.Provider, user.Subject, nil)
	if binding == "" {
		return nil, nil
	}
	return &types.LocalStateOwner{Binding: binding, ValidUntil: user.ValidUntil}, nil
}
func verifiedClaimsDeadline(claims *jwt.Claims) time.Time {
	if claims == nil || claims.ExpiresAt == nil {
		return time.Time{}
	}
	return claims.ExpiresAt.Time
}
func sessionOwnerDeadline(session *Session, tokens *scyauth.Token) time.Time {
	if session == nil {
		return time.Time{}
	}
	deadline := session.ExpiresAt
	if tokens != nil && !tokens.Expiry.IsZero() && (deadline.IsZero() || tokens.Expiry.Before(deadline)) {
		deadline = tokens.Expiry
	}
	return deadline
}
