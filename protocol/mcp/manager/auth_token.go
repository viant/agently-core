package manager

import (
	"context"
	"strings"

	authctx "github.com/viant/agently-core/internal/auth"
	token "github.com/viant/agently-core/internal/auth/token"
	authtransport "github.com/viant/mcp/client/auth/transport"
)

// UseIDToken reports whether the MCP server config prefers using an ID token
// when authenticating outbound calls to this server.
func (m *Manager) UseIDToken(ctx context.Context, serverName string) bool {
	if m == nil {
		return false
	}
	name := strings.TrimSpace(serverName)
	if name == "" {
		return false
	}
	cfg, err := m.Options(ctx, name)
	if err != nil || cfg == nil || cfg.ClientOptions == nil || cfg.ClientOptions.Auth == nil {
		return false
	}
	return cfg.ClientOptions.Auth.UseIdToken
}

// WithAuthTokenContext injects the selected auth token into context under the
// MCP auth transport key so HTTP transports can emit the appropriate Bearer header.
// This is a best-effort helper; when no token is available it returns ctx as-is.
func (m *Manager) WithAuthTokenContext(ctx context.Context, serverName string) context.Context {
	if ctx == nil || m == nil {
		return ctx
	}
	if m.tokenProvider != nil {
		if userID := strings.TrimSpace(authctx.EffectiveUserID(ctx)); userID != "" {
			provider := strings.TrimSpace(authctx.Provider(ctx))
			if provider == "" {
				provider = "oauth"
			}
			if next, err := m.tokenProvider.EnsureTokens(ctx, token.Key{
				Subject:  userID,
				Provider: provider,
			}); err == nil && next != nil {
				ctx = next
			}
		}
	}
	useID := m.UseIDToken(ctx, serverName)
	tok := authctx.MCPAuthToken(ctx, useID)
	if strings.TrimSpace(tok) == "" {
		return ctx
	}
	return context.WithValue(ctx, authtransport.ContextAuthTokenKey, tok)
}
