package manager

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
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
	debugMCPAuthEnsure(serverName, ctx, "before_ensure", m.UseIDToken(ctx, serverName))
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
				debugMCPAuthEnsure(serverName, ctx, "after_ensure_ok", m.UseIDToken(ctx, serverName))
			} else if err != nil {
				debugMCPAuthEnsureError(serverName, ctx, userID, provider, m.UseIDToken(ctx, serverName), err)
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

func debugMCPAuthEnsure(serverName string, ctx context.Context, stage string, useID bool) {
	if strings.TrimSpace(os.Getenv("AGENTLY_DEBUG_MCP_AUTH")) == "" || ctx == nil {
		return
	}
	userID := strings.TrimSpace(authctx.EffectiveUserID(ctx))
	provider := strings.TrimSpace(authctx.Provider(ctx))
	tb := authctx.TokensFromContext(ctx)
	accessFP := "none"
	idFP := "none"
	selectedFP := "none"
	if tb != nil {
		accessFP = tokenFingerprint(tb.AccessToken)
		idFP = tokenFingerprint(tb.IDToken)
	}
	if strings.TrimSpace(authctx.IDToken(ctx)) != "" && idFP == "none" {
		idFP = tokenFingerprint(authctx.IDToken(ctx))
	}
	if strings.TrimSpace(authctx.Bearer(ctx)) != "" && accessFP == "none" {
		accessFP = tokenFingerprint(authctx.Bearer(ctx))
	}
	// Best effort to infer selection intent from the currently injected transport token.
	if transportToken, _ := ctx.Value(authtransport.ContextAuthTokenKey).(string); strings.TrimSpace(transportToken) != "" {
		selectedFP = tokenFingerprint(transportToken)
	}
	fmt.Fprintf(os.Stderr, "[mcp-auth] stage=%s server=%s user=%q provider=%q useID=%v access_sha=%s id_sha=%s selected_sha=%s\n",
		strings.TrimSpace(stage), strings.TrimSpace(serverName), userID, provider, useID, accessFP, idFP, selectedFP)
}

func debugMCPAuthEnsureError(serverName string, ctx context.Context, userID, provider string, useID bool, err error) {
	if strings.TrimSpace(os.Getenv("AGENTLY_DEBUG_MCP_AUTH")) == "" {
		return
	}
	fmt.Fprintf(os.Stderr, "[mcp-auth] stage=ensure_error server=%s user=%q provider=%q useID=%v err=%q\n",
		strings.TrimSpace(serverName), strings.TrimSpace(userID), strings.TrimSpace(provider), useID, strings.TrimSpace(err.Error()))
}

func tokenFingerprint(token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return "none"
	}
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])[:12]
}
