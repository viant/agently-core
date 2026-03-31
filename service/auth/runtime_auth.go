package auth

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	iauth "github.com/viant/agently-core/internal/auth"
	scyauth "github.com/viant/scy/auth"
)

func (r *Runtime) protect(next http.Handler) http.Handler {
	if r == nil || r.cfg == nil || !r.cfg.Enabled {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method == http.MethodOptions || req.URL.Path == "/healthz" || req.URL.Path == "/health" || isSharedA2APath(req.URL.Path) {
			next.ServeHTTP(w, req)
			return
		}
		user := r.authenticate(req)
		if user == nil {
			writeRuntimeAuthDebugHeaders(w, req, r)
		}
		if user == nil {
			user = r.ensureDefaultUser(w, req)
		}
		ctx := req.Context()
		if user != nil {
			ctx = withRuntimeAuthUser(ctx, user)
		}
		if strings.HasPrefix(req.URL.Path, "/v1/api/auth/") {
			next.ServeHTTP(w, req.WithContext(ctx))
			return
		}
		if strings.HasPrefix(req.URL.Path, "/v1/") && user == nil {
			runtimeError(w, http.StatusUnauthorized, fmt.Errorf("authorization required"))
			return
		}
		next.ServeHTTP(w, req.WithContext(ctx))
	})
}

func (r *Runtime) protectAll(next http.Handler) http.Handler {
	if r == nil || r.cfg == nil || !r.cfg.Enabled {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method == http.MethodOptions || req.URL.Path == "/healthz" || req.URL.Path == "/health" || isSharedA2APath(req.URL.Path) {
			next.ServeHTTP(w, req)
			return
		}
		user := r.authenticate(req)
		if user == nil {
			writeRuntimeAuthDebugHeaders(w, req, r)
		}
		if user == nil {
			user = r.ensureDefaultUser(w, req)
		}
		ctx := req.Context()
		if user != nil {
			ctx = withRuntimeAuthUser(ctx, user)
		}
		if user == nil {
			runtimeError(w, http.StatusUnauthorized, fmt.Errorf("authorization required"))
			return
		}
		next.ServeHTTP(w, req.WithContext(ctx))
	})
}

func (r *Runtime) authenticate(req *http.Request) *runtimeAuthUser {
	if r == nil || req == nil {
		return nil
	}
	authz := strings.TrimSpace(req.Header.Get("Authorization"))
	if strings.HasPrefix(strings.ToLower(authz), "bearer ") && r.jwtVerifier != nil {
		token := strings.TrimSpace(authz[len("Bearer "):])
		if token != "" {
			if claims, err := r.jwtVerifier.VerifyClaims(req.Context(), token); err == nil && claims != nil {
				tok := &scyauth.Token{}
				tok.Token.AccessToken = token
				return &runtimeAuthUser{
					Subject:  strings.TrimSpace(firstNonEmpty(claims.Subject, claims.Username)),
					Email:    strings.TrimSpace(claims.Email),
					Provider: "jwt",
					Tokens:   tok,
				}
			}
		}
	}
	if r.sessions != nil && strings.TrimSpace(r.cfg.CookieName) != "" {
		if c, err := req.Cookie(r.cfg.CookieName); err == nil && strings.TrimSpace(c.Value) != "" {
			if sess := r.sessions.Get(req.Context(), strings.TrimSpace(c.Value)); sess != nil {
				if r.requiresOAuthTokens() && !r.ensureSessionOAuthTokens(req.Context(), sess) {
					log.Printf("[auth] session missing usable oauth tokens, invalidating session user=%q", sess.Subject)
					r.sessions.Delete(req.Context(), strings.TrimSpace(c.Value))
					return nil
				}
				if sess.Tokens != nil && !sess.Tokens.Expiry.IsZero() && !sess.Tokens.Valid() {
					refreshCtx := context.Background()
					if refreshed := r.tryRefreshToken(refreshCtx, sess); refreshed != nil {
						sess.Tokens = refreshed
					} else {
						log.Printf("[auth] token expired and refresh failed, invalidating session user=%q", sess.Subject)
						r.sessions.Delete(refreshCtx, c.Value)
						return nil
					}
				}
				return &runtimeAuthUser{
					Subject:  strings.TrimSpace(firstNonEmpty(sess.Subject, sess.Username)),
					Email:    strings.TrimSpace(sess.Email),
					Provider: strings.TrimSpace(sess.Provider),
					Tokens:   sess.Tokens,
				}
			}
		}
	}
	return nil
}

func writeRuntimeAuthDebugHeaders(w http.ResponseWriter, req *http.Request, r *Runtime) {
	if w == nil || req == nil {
		return
	}
	authz := strings.TrimSpace(req.Header.Get("Authorization"))
	if !strings.HasPrefix(strings.ToLower(authz), "bearer ") {
		w.Header().Set("X-Auth-Reason", "missing_or_non_bearer")
		return
	}
	token := strings.TrimSpace(authz[len("Bearer "):])
	if token == "" {
		w.Header().Set("X-Auth-Reason", "empty_bearer")
		return
	}
	claims := parseJWTClaims(token)
	w.Header().Set("X-Auth-Token-Iss", truncateHeader(claimString(claims, "iss"), 180))
	w.Header().Set("X-Auth-Token-Aud", truncateHeader(runtimeClaimStrings(claims["aud"]), 180))
	if r == nil || r.jwtVerifier == nil {
		w.Header().Set("X-Auth-Reason", "bearer_not_supported_no_jwt_verifier")
		return
	}
	if _, err := r.jwtVerifier.VerifyClaims(req.Context(), token); err != nil {
		w.Header().Set("X-Auth-Reason", "bearer_jwt_verify_failed")
		w.Header().Set("X-Auth-Verify-Error", truncateHeader(err.Error(), 180))
		return
	}
	w.Header().Set("X-Auth-Reason", "bearer_verified_but_no_runtime_user")
}

func truncateHeader(value string, max int) string {
	value = strings.TrimSpace(value)
	if max <= 0 || len(value) <= max {
		return value
	}
	return value[:max]
}

func runtimeClaimStrings(value interface{}) string {
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

func isSharedA2APath(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}
	if path == "/.well-known/agent.json" {
		return true
	}
	if path == "/v1/message:send" || path == "/v1/message:stream" {
		return true
	}
	return false
}

func (r *Runtime) requiresOAuthTokens() bool {
	if r == nil || r.cfg == nil || r.cfg.OAuth == nil {
		return false
	}
	mode := strings.ToLower(strings.TrimSpace(r.cfg.OAuth.Mode))
	return mode == "bff" || mode == "mixed"
}

func (r *Runtime) ensureDefaultUser(w http.ResponseWriter, req *http.Request) *runtimeAuthUser {
	if r == nil || r.sessions == nil || r.cfg == nil {
		return nil
	}
	if r.cfg.Local == nil || !r.cfg.Local.Enabled {
		return nil
	}
	if r.cfg.OAuth != nil {
		mode := strings.ToLower(strings.TrimSpace(r.cfg.OAuth.Mode))
		if mode == "bff" || mode == "mixed" || mode == "oidc" || mode == "spa" || mode == "bearer" {
			return nil
		}
	}
	username := strings.TrimSpace(r.cfg.DefaultUsername)
	if username == "" {
		return nil
	}
	session := &Session{
		ID:        fmt.Sprintf("auto-%d", time.Now().UnixNano()),
		Username:  username,
		Subject:   username,
		Provider:  "local",
		CreatedAt: time.Now(),
	}
	r.sessions.Put(req.Context(), session)
	writeSessionCookie(w, r.cfg, r.sessions, session.ID)
	return &runtimeAuthUser{Subject: username, Provider: "local"}
}

func runtimeUserInfo(ctx context.Context) *UserInfo {
	return fromInternalUserInfo(iauth.User(ctx))
}
