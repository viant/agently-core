package auth

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"

	iauth "github.com/viant/agently-core/internal/auth"
	token "github.com/viant/agently-core/internal/auth/token"
	scyauth "github.com/viant/scy/auth"
	"golang.org/x/oauth2"
)

// Protect returns middleware that extracts auth credentials from the request
// (Bearer token or session cookie) and populates the request context with
// authenticated user identity and tokens.
//
// Requests to /v1/api/auth/* and OPTIONS are passed through without auth.
// When JWT auth is configured, Bearer tokens are cryptographically verified.
//
// ProtectWithTokenProvider is like Protect but also stores session tokens in the
// given token.Provider so they are available for subsequent requests from that user.
func ProtectWithTokenProvider(cfg *Config, sessions *Manager, tp token.Provider, opts ...ProtectOption) func(http.Handler) http.Handler {
	inner := Protect(cfg, sessions, opts...)
	if tp == nil {
		return inner
	}
	return func(next http.Handler) http.Handler {
		return inner(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			// If session cookie path resolved tokens, store them in the provider.
			if tok := iauth.TokensFromContext(ctx); tok != nil {
				userID := iauth.EffectiveUserID(ctx)
				if userID != "" {
					_ = tp.Store(ctx, token.Key{Subject: userID, Provider: "default"}, tok)
				}
			}
			next.ServeHTTP(w, r)
		}))
	}
}

// ProtectOption customises the Protect middleware.
type ProtectOption func(*protectConfig)

type protectConfig struct {
	jwtService *JWTService
}

// WithJWTService injects a pre-initialised JWTService for Bearer token verification.
func WithJWTService(j *JWTService) ProtectOption {
	return func(c *protectConfig) { c.jwtService = j }
}

func Protect(cfg *Config, sessions *Manager, opts ...ProtectOption) func(http.Handler) http.Handler {
	pc := &protectConfig{}
	for _, o := range opts {
		o(pc)
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// CORS preflight always passes through immediately.
			if r.Method == http.MethodOptions {
				next.ServeHTTP(w, r)
				return
			}
			if cfg == nil || !cfg.Enabled {
				next.ServeHTTP(w, r)
				return
			}

			// Auth endpoints still get credential extraction (for /me) but
			// unauthenticated requests are not rejected.
			isAuthPath := strings.HasPrefix(r.URL.Path, "/v1/api/auth/")

			ctx := r.Context()
			authenticated := false

			// Try Bearer token first.
			// Always accept a valid JWT when a JWTService is configured,
			// even if the primary auth mode is cookie/local/BFF.
			bearerAccepted := cfg.IsBearerAccepted() || pc.jwtService != nil
			if bearerAccepted {
				if bearerTok := extractBearer(r); bearerTok != "" {
					// When a JWTService is available, verify the token cryptographically.
					if pc.jwtService != nil {
						ui, err := pc.jwtService.Verify(ctx, bearerTok)
						if err != nil {
							if !isAuthPath {
								http.Error(w, `{"error":"invalid or expired token"}`, http.StatusUnauthorized)
								return
							}
							// For auth paths, skip invalid tokens silently.
						} else {
							ctx = iauth.WithBearer(ctx, bearerTok)
							if ui != nil {
								ctx = iauth.WithUserInfo(ctx, toInternalUserInfo(ui))
							}
							authenticated = true
						}
					} else {
						// Non-JWT mode: trust upstream verification.
						ctx = iauth.WithBearer(ctx, bearerTok)
						if ui := parseJWTUserInfo(bearerTok); ui != nil {
							ctx = iauth.WithUserInfo(ctx, ui)
						}
						authenticated = true
					}
				}
			}

			// For JWT-only mode (no cookies accepted), reject unauthenticated
			// requests immediately. When cookies are also accepted, fall through
			// to the session cookie check below.
			if !authenticated && !isAuthPath && cfg.IsJWTAuth() && !cfg.IsCookieAccepted() {
				http.Error(w, `{"error":"authorization required"}`, http.StatusUnauthorized)
				return
			}

			// Try session cookie.
			if !authenticated && cfg.IsCookieAccepted() && sessions != nil && cfg.CookieName != "" {
				if c, err := r.Cookie(cfg.CookieName); err == nil && c.Value != "" {
					if sess := sessions.Get(ctx, c.Value); sess != nil {
						if requiresTokenBackedCookie(cfg) && (sess.Tokens == nil || strings.TrimSpace(sess.Tokens.AccessToken) == "" && strings.TrimSpace(sess.Tokens.IDToken) == "") {
							if !isAuthPath {
								http.Error(w, `{"error":"authorization required"}`, http.StatusUnauthorized)
								return
							}
						} else {
							subject := strings.TrimSpace(firstNonEmpty(sess.Subject, sess.Username))
							email := strings.TrimSpace(sess.Email)
							ctx = iauth.WithUserInfo(ctx, &iauth.UserInfo{
								Subject: subject,
								Email:   email,
							})
							if sess.Tokens != nil {
								ctx = iauth.WithTokens(ctx, sess.Tokens)
								if sess.Tokens.AccessToken != "" {
									ctx = iauth.WithBearer(ctx, sess.Tokens.AccessToken)
								}
								if sess.Tokens.IDToken != "" {
									ctx = iauth.WithIDToken(ctx, sess.Tokens.IDToken)
								}
							}
							authenticated = true
						}
					}
				}
			}

			// Reject unauthenticated non-auth requests when auth is enabled.
			if !authenticated && !isAuthPath {
				http.Error(w, `{"error":"authorization required"}`, http.StatusUnauthorized)
				return
			}

			// Ensure fallback user identity from config.
			if iauth.User(ctx) == nil && cfg != nil && cfg.IsLocalAuth() {
				if u := strings.TrimSpace(cfg.DefaultUsername); u != "" {
					ctx = iauth.WithUserInfo(ctx, &iauth.UserInfo{Subject: u})
				}
			}

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func requiresTokenBackedCookie(cfg *Config) bool {
	if cfg == nil || !cfg.Enabled || cfg.OAuth == nil {
		return false
	}
	if cfg.Local != nil && cfg.Local.Enabled {
		return false
	}
	mode := strings.ToLower(strings.TrimSpace(cfg.OAuth.Mode))
	return mode == "bff"
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

// parseJWTUserInfo does a best-effort extraction of subject/email from a JWT
// without cryptographic verification. Full verification (JWKS) should be done
// by the host application or an upstream proxy.
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

// decodeJWTSegment base64-decodes a JWT segment and returns its claims.
func decodeJWTSegment(seg string) (map[string]interface{}, error) {
	switch len(seg) % 4 {
	case 2:
		seg += "=="
	case 3:
		seg += "="
	}
	data, err := base64.URLEncoding.DecodeString(seg)
	if err != nil {
		return nil, err
	}
	var claims map[string]interface{}
	if err := json.Unmarshal(data, &claims); err != nil {
		return nil, err
	}
	return claims, nil
}

func newTokenBundle(access, id, refresh string) *scyauth.Token {
	return &scyauth.Token{
		Token: oauth2.Token{
			AccessToken:  access,
			RefreshToken: refresh,
		},
		IDToken: id,
	}
}
