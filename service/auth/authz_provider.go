package auth

import (
	"context"
	"strings"
	"time"

	"github.com/viant/authz"
	"github.com/viant/authz/gating"
	authzoauth "github.com/viant/authz/oauth"
)

// AuthzIDTokenContext forwards only the ID token established by trusted host
// ingress to a shared authz OAuth provider. Missing ID tokens stay missing;
// access-token fallback is deliberately prohibited for user-info ACL facts.
func AuthzIDTokenContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	token := strings.TrimSpace(IDToken(ctx))
	if token == "" {
		return ctx
	}
	return authzoauth.WithBearer(ctx, token)
}

// IDTokenFactProvider adapts authz's verified OAuth provider to the host's
// existing request context. The wrapped source still verifies the credential.
type IDTokenFactProvider struct{ Source authz.Provider }

func (p IDTokenFactProvider) Resolve(ctx context.Context) (authz.Facts, error) {
	if p.Source == nil || IDToken(ctx) == "" {
		return authz.Facts{}, authz.ErrDenied
	}
	return p.Source.Resolve(AuthzIDTokenContext(ctx))
}

// IDTokenIdentityProvider adapts one verified account authority to both ACL
// facts and gate principals. Both reads use the host-established ID token;
// a browser supplied access token is never substituted for it.
type IDTokenIdentityProvider struct {
	Source interface {
		authz.Provider
		gating.PrincipalResolver
	}
}

var _ authz.Provider = IDTokenIdentityProvider{}
var _ gating.PrincipalResolver = IDTokenIdentityProvider{}

func (p IDTokenIdentityProvider) Resolve(ctx context.Context) (authz.Facts, error) {
	if p.Source == nil || IDToken(ctx) == "" {
		return authz.Facts{}, authz.ErrDenied
	}
	return p.Source.Resolve(AuthzIDTokenContext(ctx))
}

func (p IDTokenIdentityProvider) ResolvePrincipal(ctx context.Context) (gating.Principal, error) {
	if p.Source == nil || IDToken(ctx) == "" {
		return gating.Principal{}, authz.ErrDenied
	}
	return p.Source.ResolvePrincipal(AuthzIDTokenContext(ctx))
}

type AccountSource interface {
	Account(context.Context, authz.Facts) (string, error)
}

func IDTokenAccountResolver(source AccountSource) func(context.Context, authz.Facts) (string, error) {
	return func(ctx context.Context, facts authz.Facts) (string, error) {
		if source == nil || IDToken(ctx) == "" {
			return "", authz.ErrDenied
		}
		return source.Account(AuthzIDTokenContext(ctx), facts)
	}
}

type AuthorityRevisionSource interface {
	AuthorityRevision(context.Context, authz.Facts, string) (string, time.Time, error)
}

func IDTokenAuthorityRevisionResolver(source AuthorityRevisionSource) func(context.Context, authz.Facts, string) (string, time.Time, error) {
	return func(ctx context.Context, facts authz.Facts, accountID string) (string, time.Time, error) {
		if source == nil || IDToken(ctx) == "" {
			return "", time.Time{}, authz.ErrDenied
		}
		return source.AuthorityRevision(AuthzIDTokenContext(ctx), facts, accountID)
	}
}
