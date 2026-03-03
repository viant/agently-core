package a2a

import (
	"net/http"
	"strings"

	iauth "github.com/viant/agently-core/internal/auth"
	agentmodel "github.com/viant/agently-core/protocol/agent"
)

// AuthMiddleware returns HTTP middleware that enforces A2A authentication
// based on the agent's A2AAuth configuration. It validates Bearer tokens
// and checks required scopes.
func AuthMiddleware(authCfg *agentmodel.A2AAuth) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if authCfg == nil || !authCfg.Enabled {
				next.ServeHTTP(w, r)
				return
			}

			// Skip auth for excluded prefixes.
			if authCfg.ExcludePrefix != "" && strings.HasPrefix(r.URL.Path, authCfg.ExcludePrefix) {
				next.ServeHTTP(w, r)
				return
			}

			// Skip OPTIONS for CORS preflight.
			if r.Method == http.MethodOptions {
				next.ServeHTTP(w, r)
				return
			}

			// Extract Bearer token.
			token := extractBearer(r)
			if token == "" {
				http.Error(w, `{"error":"missing bearer token"}`, http.StatusUnauthorized)
				return
			}

			// Store token in context for downstream use.
			ctx := iauth.WithBearer(r.Context(), token)

			// Parse user info from JWT if available.
			if ui := parseJWTUserInfo(token); ui != nil {
				ctx = iauth.WithUserInfo(ctx, ui)
			}

			// If useIDToken is set, also store as ID token for MCP auth propagation.
			if authCfg.UseIDToken {
				ctx = iauth.WithIDToken(ctx, token)
			}

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// extractBearer extracts a Bearer token from the Authorization header.
func extractBearer(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
	}
	return ""
}

// parseJWTUserInfo does best-effort extraction of subject/email from a JWT.
func parseJWTUserInfo(token string) *iauth.UserInfo {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil
	}
	payload, err := decodeJWTSegment(parts[1])
	if err != nil {
		return nil
	}
	ui := &iauth.UserInfo{}
	if sub, ok := payload["sub"].(string); ok {
		ui.Subject = sub
	}
	if email, ok := payload["email"].(string); ok {
		ui.Email = email
	}
	if ui.Subject == "" && ui.Email == "" {
		return nil
	}
	return ui
}
