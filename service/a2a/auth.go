package a2a

import (
	"log"
	"net/http"
	"strings"

	iauth "github.com/viant/agently-core/internal/auth"
	agentmodel "github.com/viant/agently-core/protocol/agent"
	svcauth "github.com/viant/agently-core/service/auth"
)

// AuthMiddleware returns HTTP middleware that enforces A2A authentication
// based on the agent's A2AAuth configuration. It validates Bearer tokens
// and checks required scopes.
func AuthMiddleware(authCfg *agentmodel.A2AAuth, jwtSvc *svcauth.JWTService) func(http.Handler) http.Handler {
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

			ctx := iauth.WithBearer(r.Context(), token)
			if jwtSvc != nil {
				ui, err := jwtSvc.Verify(ctx, token)
				if err != nil {
					log.Printf("[a2a-auth] reject path=%q reason=jwt_verify_failed", r.URL.Path)
					http.Error(w, `{"error":"invalid or expired token"}`, http.StatusUnauthorized)
					return
				}
				if ui != nil {
					ctx = iauth.WithUserInfo(ctx, ui)
					log.Printf("[a2a-auth] accept path=%q user=%q mode=verified use_id_token=%v", r.URL.Path, firstNonEmpty(ui.Subject, ui.Email), authCfg.UseIDToken)
				} else {
					log.Printf("[a2a-auth] accept path=%q user=%q mode=verified use_id_token=%v", r.URL.Path, "", authCfg.UseIDToken)
				}
			} else if ui := parseJWTUserInfo(token); ui != nil {
				// Fallback for deployments that rely on upstream verification.
				ctx = iauth.WithUserInfo(ctx, ui)
				log.Printf("[a2a-auth] accept path=%q user=%q mode=best_effort use_id_token=%v", r.URL.Path, firstNonEmpty(ui.Subject, ui.Email), authCfg.UseIDToken)
			} else {
				log.Printf("[a2a-auth] accept path=%q user=%q mode=unverified use_id_token=%v", r.URL.Path, "", authCfg.UseIDToken)
			}

			// If useIDToken is set, also store as ID token for MCP auth propagation.
			if authCfg.UseIDToken {
				ctx = iauth.WithIDToken(ctx, token)
			}

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
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
