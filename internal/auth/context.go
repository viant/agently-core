package auth

import (
	"context"
	shared "github.com/viant/mcp/client/auth/link"
	scyauth "github.com/viant/scy/auth"
	"strings"
)

// UserInfo is transport partitioning metadata; authoritative application facts
// continue to come from Core's configured identity and authorization providers.
type UserInfo = shared.UserInfo

func WithTokens(ctx context.Context, value *scyauth.Token) context.Context {
	return shared.WithTokens(ctx, value)
}
func TokensFromContext(ctx context.Context) *scyauth.Token { return shared.TokensFromContext(ctx) }
func WithoutTokens(ctx context.Context) context.Context    { return shared.WithoutTokens(ctx) }
func MCPAuthToken(ctx context.Context, useIDToken bool) string {
	return shared.MCPAuthToken(ctx, useIDToken)
}
func WithBearer(ctx context.Context, value string) context.Context {
	return shared.WithBearer(ctx, value)
}
func Bearer(ctx context.Context) string { return shared.Bearer(ctx) }
func WithIDToken(ctx context.Context, value string) context.Context {
	return shared.WithIDToken(ctx, value)
}
func IDToken(ctx context.Context) string { return shared.IDToken(ctx) }
func WithProvider(ctx context.Context, value string) context.Context {
	return shared.WithProvider(ctx, value)
}
func Provider(ctx context.Context) string { return shared.Provider(ctx) }
func WithUserInfo(ctx context.Context, value *UserInfo) context.Context {
	return shared.WithUserInfo(ctx, value)
}
func User(ctx context.Context) *UserInfo { return shared.UserFromContext(ctx) }
func WithCanonicalUserID(ctx context.Context, value string) context.Context {
	return shared.WithCanonicalUserID(ctx, value)
}
func CanonicalUserID(ctx context.Context) string { return shared.CanonicalUserID(ctx) }
func EffectiveUserID(ctx context.Context) string { return shared.EffectiveUserID(ctx) }

// EnsureUser populates a user identity in context when missing using config
// fallbacks (e.g., local mode default username). Returns the original context
// when no action is needed.
func EnsureUser(ctx context.Context, cfg *Config) context.Context {
	if ctx == nil {
		return ctx
	}
	if ui := User(ctx); ui != nil {
		if strings.TrimSpace(ui.Subject) != "" || strings.TrimSpace(ui.Email) != "" {
			return ctx
		}
	}
	if cfg != nil && cfg.IsLocalAuth() {
		if u := strings.TrimSpace(cfg.DefaultUsername); u != "" {
			return WithUserInfo(ctx, &UserInfo{Subject: u})
		}
	}
	return ctx
}
