package auth

import (
	"context"

	iauth "github.com/viant/agently-core/internal/auth"
	scyauth "github.com/viant/scy/auth"
)

// InjectUser stores user identity in context using the agently-core auth key.
// External middleware (outside this module) can call this to bridge their own
// auth context into the context key that EffectiveUserID reads.
func InjectUser(ctx context.Context, subject string) context.Context {
	if subject == "" {
		return ctx
	}
	return iauth.WithUserInfo(ctx, &iauth.UserInfo{Subject: subject})
}

// EffectiveUserID returns a stable user identifier from context.
// Delegates to the internal auth package.
func EffectiveUserID(ctx context.Context) string {
	return iauth.EffectiveUserID(ctx)
}

// MCPAuthToken selects a single token string suitable for outbound MCP calls.
// Delegates to the internal auth package.
func MCPAuthToken(ctx context.Context, useIDToken bool) string {
	return iauth.MCPAuthToken(ctx, useIDToken)
}

// InjectTokens stores OAuth tokens in context so that MCPAuthToken and
// downstream MCP clients can forward the logged-in user's token.
// External auth middleware (outside this module) should call this after
// authenticating the user from a session or JWT.
func InjectTokens(ctx context.Context, tokens *scyauth.Token) context.Context {
	if ctx == nil || tokens == nil {
		return ctx
	}
	ctx = iauth.WithTokens(ctx, tokens)
	if tokens.AccessToken != "" {
		ctx = iauth.WithBearer(ctx, tokens.AccessToken)
	}
	if tokens.IDToken != "" {
		ctx = iauth.WithIDToken(ctx, tokens.IDToken)
	}
	return ctx
}
