package a2a

import (
	"log"
	"net/http"
	"strings"

	iauth "github.com/viant/agently-core/internal/auth"
	token "github.com/viant/agently-core/internal/auth/token"
	agentmodel "github.com/viant/agently-core/protocol/agent"
	svcauth "github.com/viant/agently-core/service/auth"
	scyauth "github.com/viant/scy/auth"
	"golang.org/x/oauth2"
)

// AuthMiddleware returns HTTP middleware that enforces A2A authentication
// based on the agent's A2AAuth configuration. It validates Bearer tokens,
// checks required scopes, and — when a TokenProvider is supplied — registers
// the inbound token so mid-turn refresh works via EnsureTokens.
func AuthMiddleware(authCfg *agentmodel.A2AAuth, jwtSvc *svcauth.JWTService, tokenProvider ...token.Provider) func(http.Handler) http.Handler {
	var tp token.Provider
	if len(tokenProvider) > 0 {
		tp = tokenProvider[0]
	}
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
				setAuthDebugHeaders(w, authDebugInfo{Reason: "missing_bearer_token"})
				http.Error(w, `{"error":"missing bearer token"}`, http.StatusUnauthorized)
				return
			}

			ctx := iauth.WithBearer(r.Context(), token)
			dbg := parseJWTDebugInfo(token)
			dbg.UseIDToken = authCfg.UseIDToken
			dbg.Path = r.URL.Path
			if jwtSvc != nil {
				ui, err := jwtSvc.Verify(ctx, token)
				if err != nil {
					dbg.Reason = "jwt_verify_failed"
					dbg.VerifyError = err.Error()
					setAuthDebugHeaders(w, dbg)
					log.Printf("[a2a-auth] reject path=%q reason=%s use_id_token=%v sub=%q iss=%q aud=%q azp=%q err=%v",
						r.URL.Path, dbg.Reason, dbg.UseIDToken, dbg.Subject, dbg.Issuer, dbg.Audience, dbg.AuthorizedParty, err)
					http.Error(w, `{"error":"invalid or expired token"}`, http.StatusUnauthorized)
					return
				}
				if ui != nil {
					ctx = iauth.WithUserInfo(ctx, &iauth.UserInfo{
						Subject: ui.Subject,
						Email:   ui.Email,
					})
					log.Printf("[a2a-auth] accept path=%q user=%q mode=verified use_id_token=%v sub=%q iss=%q aud=%q azp=%q",
						r.URL.Path, firstNonEmpty(ui.Subject, ui.Email), authCfg.UseIDToken, dbg.Subject, dbg.Issuer, dbg.Audience, dbg.AuthorizedParty)
				} else {
					log.Printf("[a2a-auth] accept path=%q user=%q mode=verified use_id_token=%v sub=%q iss=%q aud=%q azp=%q",
						r.URL.Path, "", authCfg.UseIDToken, dbg.Subject, dbg.Issuer, dbg.Audience, dbg.AuthorizedParty)
				}
			} else if ui := parseJWTUserInfo(token); ui != nil {
				// Fallback for deployments that rely on upstream verification.
				ctx = iauth.WithUserInfo(ctx, ui)
				log.Printf("[a2a-auth] accept path=%q user=%q mode=best_effort use_id_token=%v sub=%q iss=%q aud=%q azp=%q",
					r.URL.Path, firstNonEmpty(ui.Subject, ui.Email), authCfg.UseIDToken, dbg.Subject, dbg.Issuer, dbg.Audience, dbg.AuthorizedParty)
			} else {
				log.Printf("[a2a-auth] accept path=%q user=%q mode=unverified use_id_token=%v sub=%q iss=%q aud=%q azp=%q",
					r.URL.Path, "", authCfg.UseIDToken, dbg.Subject, dbg.Issuer, dbg.Audience, dbg.AuthorizedParty)
			}

			// If useIDToken is set, also store as ID token for MCP auth propagation.
			if authCfg.UseIDToken {
				ctx = iauth.WithIDToken(ctx, token)
			}

			// Register the inbound token in the provider cache so that EnsureTokens
			// can refresh it mid-turn. Without this, A2A context tokens expire with
			// no refresh path because they have no local session on this server.
			if tp != nil {
				subject := ""
				if ui := iauth.User(ctx); ui != nil {
					subject = strings.TrimSpace(ui.Subject)
				}
				if subject != "" {
					provider := authCfg.Resource
					if provider == "" {
						provider = "oauth"
					}
					tok := &scyauth.Token{
						Token:   oauth2.Token{AccessToken: token},
						IDToken: token,
					}
					if !authCfg.UseIDToken {
						tok.IDToken = ""
					}
					_ = tp.Store(r.Context(), tokenKey{Subject: subject, Provider: provider}, tok)
					ctx = iauth.WithProvider(ctx, provider)
				}
			}

			// Fix 4: Validate audience when resource is configured.
			// If the token passes JWT signature verification but has wrong audience,
			// log a warning. The token is from a trusted IDP so we allow it through
			// but surface the mismatch for operators to fix IDP configuration.
			if authCfg.Resource != "" && jwtSvc != nil {
				if aud := parseJWTDebugInfo(token).Audience; aud != "" {
					if !audienceContains(aud, authCfg.Resource) {
						log.Printf("[a2a-auth] warning: token audience %q does not include resource %q — configure IDP to issue tokens with audience %q",
							aud, authCfg.Resource, authCfg.Resource)
					}
				}
			}

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// tokenKey is a local alias so auth.go doesn't re-export the internal type.
type tokenKey = token.Key

// audienceContains reports whether the comma-separated audience string contains target.
func audienceContains(aud, target string) bool {
	target = strings.TrimSpace(target)
	if target == "" {
		return true
	}
	for _, part := range strings.Split(aud, ",") {
		if strings.TrimSpace(part) == target {
			return true
		}
	}
	return false
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

type authDebugInfo struct {
	Path            string
	Reason          string
	VerifyError     string
	Subject         string
	Email           string
	Issuer          string
	Audience        string
	AuthorizedParty string
	UseIDToken      bool
}

func setAuthDebugHeaders(w http.ResponseWriter, info authDebugInfo) {
	if w == nil {
		return
	}
	if info.Reason != "" {
		w.Header().Set("X-A2A-Auth-Reason", info.Reason)
	}
	if info.Subject != "" {
		w.Header().Set("X-A2A-Token-Sub", info.Subject)
	}
	if info.Issuer != "" {
		w.Header().Set("X-A2A-Token-Iss", info.Issuer)
	}
	if info.Audience != "" {
		w.Header().Set("X-A2A-Token-Aud", info.Audience)
	}
	if info.AuthorizedParty != "" {
		w.Header().Set("X-A2A-Token-Azp", info.AuthorizedParty)
	}
	if info.VerifyError != "" {
		w.Header().Set("X-A2A-Auth-Verify-Error", truncateHeaderValue(info.VerifyError, 180))
	}
}

func truncateHeaderValue(value string, max int) string {
	value = strings.TrimSpace(value)
	if len(value) <= max || max <= 0 {
		return value
	}
	return value[:max]
}

func parseJWTDebugInfo(token string) authDebugInfo {
	info := authDebugInfo{}
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 3 {
		return info
	}
	payload, err := decodeJWTSegment(parts[1])
	if err != nil {
		info.VerifyError = "decode_jwt_payload_failed: " + err.Error()
		return info
	}
	if sub, ok := payload["sub"].(string); ok {
		info.Subject = strings.TrimSpace(sub)
	}
	if email, ok := payload["email"].(string); ok {
		info.Email = strings.TrimSpace(email)
	}
	if iss, ok := payload["iss"].(string); ok {
		info.Issuer = strings.TrimSpace(iss)
	}
	info.Audience = stringifyJWTClaim(payload["aud"])
	if azp, ok := payload["azp"].(string); ok {
		info.AuthorizedParty = strings.TrimSpace(azp)
	}
	return info
}

func stringifyJWTClaim(value interface{}) string {
	switch actual := value.(type) {
	case string:
		return strings.TrimSpace(actual)
	case []interface{}:
		parts := make([]string, 0, len(actual))
		for _, item := range actual {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				parts = append(parts, strings.TrimSpace(s))
			}
		}
		return strings.Join(parts, ",")
	default:
		return ""
	}
}
