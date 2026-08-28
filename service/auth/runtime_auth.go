package auth

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	iauth "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/internal/authlog"
	"github.com/viant/agently-core/internal/logx"
	scyauth "github.com/viant/scy/auth"
)

const authRefreshRequestTimeout = 5 * time.Second

func (r *Runtime) protect(next http.Handler) http.Handler {
	if r == nil || r.cfg == nil || !r.cfg.Enabled {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method == http.MethodOptions || req.URL.Path == "/healthz" || req.URL.Path == "/health" || isSharedA2APath(req.URL.Path) {
			next.ServeHTTP(w, req)
			return
		}
		user, confirmedMissing := r.authenticate(req)
		if user == nil && !confirmedMissing {
			writeRuntimeAuthDebugHeaders(w, req, r)
		}
		if user == nil && !confirmedMissing {
			user = r.ensureDefaultUser(w, req)
		}
		ctx := req.Context()
		if confirmedMissing {
			ctx = context.WithValue(ctx, runtimeConfirmedMissingContextKey{}, true)
		}
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
		user, confirmedMissing := r.authenticate(req)
		if user == nil && !confirmedMissing {
			writeRuntimeAuthDebugHeaders(w, req, r)
		}
		if user == nil && !confirmedMissing {
			user = r.ensureDefaultUser(w, req)
		}
		ctx := req.Context()
		if confirmedMissing {
			ctx = context.WithValue(ctx, runtimeConfirmedMissingContextKey{}, true)
		}
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

type runtimeConfirmedMissingContextKey struct{}

func runtimeConfirmedMissing(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	confirmed, _ := ctx.Value(runtimeConfirmedMissingContextKey{}).(bool)
	return confirmed
}

func (r *Runtime) authenticate(req *http.Request) (*runtimeAuthUser, bool) {
	if r == nil || req == nil {
		return nil, false
	}
	authz := strings.TrimSpace(req.Header.Get("Authorization"))
	if strings.HasPrefix(strings.ToLower(authz), "bearer ") && r.jwtVerifier != nil {
		token := strings.TrimSpace(authz[len("Bearer "):])
		if token != "" {
			if claims, err := r.jwtVerifier.VerifyClaims(req.Context(), token); err == nil && claims != nil {
				if err := validateConfiguredOAuthScopes(runtimeOAuthClient(r), nil, token); err != nil {
					return nil, false
				}
				tok := &scyauth.Token{}
				tok.Token.AccessToken = token
				tok.IDToken = token
				subject := strings.TrimSpace(firstNonEmpty(claims.Subject, claims.Email))
				return &runtimeAuthUser{
					EffectiveUserID: subject,
					CanonicalUserID: r.resolveBearerCanonicalUserID(req.Context(), subject, strings.TrimSpace(claims.Email)),
					Subject:         subject,
					Email:           strings.TrimSpace(claims.Email),
					Provider:        "jwt",
					Tokens:          tok,
				}, false
			}
		}
	}
	if r.sessions != nil && strings.TrimSpace(r.cfg.CookieName) != "" {
		if c, err := req.Cookie(r.cfg.CookieName); err == nil && strings.TrimSpace(c.Value) != "" {
			if sess := r.sessions.Get(req.Context(), strings.TrimSpace(c.Value)); sess != nil {
				r.ensureSessionCanonicalUserID(req.Context(), sess)
				if !r.requiresOAuthTokensForSession(sess) {
					return runtimeAuthUserFromSession(sess, sess.Tokens), false
				}

				availability := r.ensureSessionOAuthTokens(req.Context(), sess)
				switch availability.state {
				case tokenAvailable:
					return runtimeAuthUserFromSession(sess, availability.token), false
				case tokenConfirmedMissing:
					r.clearRefreshRetryAt(sess)
					r.deleteSessionDurable(req.Context(), strings.TrimSpace(c.Value))
					return nil, true
				case tokenPreserveWithoutInjection:
					authlog.Log(req.Context(), authlog.Event{
						Op:             "session_token_resolve",
						UserID:         strings.TrimSpace(sess.UserID),
						Provider:       strings.TrimSpace(sess.Provider),
						Classification: "token_unavailable",
						Action:         "preserve_no_inject",
					})
				}

				if retryAt := r.loadRefreshRetryAt(sess); !retryAt.IsZero() && time.Now().Before(retryAt) {
					if r.shouldLogRefreshRetry(sess, retryAt) {
						logx.Debugf("token-refresh", "cooldown active user_id=%q provider=%q retry_at=%q",
							strings.TrimSpace(sess.UserID), strings.TrimSpace(sess.Provider), retryAt.UTC().Format(time.RFC3339))
					}
					return runtimeAuthUserFromSession(sess, nil), false
				}

				if sess.Tokens == nil || strings.TrimSpace(sess.Tokens.RefreshToken) == "" {
					return runtimeAuthUserFromSession(sess, nil), false
				}

				refreshCtx := req.Context()
				var refreshCancel context.CancelFunc
				if _, hasDeadline := refreshCtx.Deadline(); hasDeadline {
					refreshCtx, refreshCancel = context.WithCancel(refreshCtx)
				} else {
					refreshCtx, refreshCancel = context.WithTimeout(refreshCtx, authRefreshRequestTimeout)
				}
				refreshed := func() tokenAvailabilityResult {
					defer refreshCancel()
					return r.tryRefreshToken(refreshCtx, sess)
				}()
				switch refreshed.state {
				case tokenAvailable:
					r.clearRefreshRetryAt(sess)
					return runtimeAuthUserFromSession(sess, refreshed.token), false
				case tokenConfirmedMissing:
					r.clearRefreshRetryAt(sess)
					r.deleteSessionDurable(req.Context(), strings.TrimSpace(c.Value))
					return nil, true
				default:
					retryAt := r.loadRefreshRetryAt(sess)
					if retryAt.IsZero() {
						retryAt = time.Now().Add(transientRefreshRetryWindow)
						r.storeRefreshRetryAt(sess, retryAt)
						r.putSessionDurable(req.Context(), sess)
					}
					if r.shouldLogRefreshRetry(sess, retryAt) {
						logx.Debugf("token-refresh", "refresh failed; preserving without injection user_id=%q provider=%q retry_at=%q",
							strings.TrimSpace(sess.UserID), strings.TrimSpace(sess.Provider), retryAt.UTC().Format(time.RFC3339))
					}
					return runtimeAuthUserFromSession(sess, nil), false
				}
			}
		}
	}
	return nil, false
}

func runtimeOAuthClient(r *Runtime) *OAuthClient {
	if r == nil || r.cfg == nil || r.cfg.OAuth == nil {
		return nil
	}
	return r.cfg.OAuth.Client
}

func scopeValidationTokens(sess *Session) []string {
	if sess == nil || sess.Tokens == nil {
		return nil
	}
	return []string{
		strings.TrimSpace(sess.Tokens.IDToken),
		strings.TrimSpace(sess.Tokens.AccessToken),
		strings.TrimSpace(sess.Tokens.RefreshToken),
	}
}

func runtimeAuthUserFromSession(sess *Session, tokens *scyauth.Token) *runtimeAuthUser {
	if sess == nil {
		return nil
	}
	// Keep request-scoped identity on the raw provider subject/email/username.
	// See the Session identity split in session.go: sess.UserID is canonical
	// for token/session persistence, while request EffectiveUserID must stay
	// subject-compatible for ownership filters such as created_by_user_id.
	// Do not replace this with sess.EffectiveUserID(): that prefers the
	// canonical users.id UUID and breaks legacy ownership filters where
	// created_by_user_id stores the provider subject, for example
	// "agently_scheduler".
	requestUserID := strings.TrimSpace(firstNonEmpty(sess.Subject, sess.Email, sess.Username))
	return &runtimeAuthUser{
		EffectiveUserID: requestUserID,
		CanonicalUserID: strings.TrimSpace(sess.UserID),
		Subject:         strings.TrimSpace(firstNonEmpty(sess.Subject, sess.Email, sess.Username)),
		Email:           strings.TrimSpace(sess.Email),
		Provider:        strings.TrimSpace(sess.Provider),
		Tokens:          tokens,
	}
}

// resolveBearerCanonicalUserID maps a verified bearer identity to the
// canonical users.id through the same shared resolver as the session path.
// A miss returns empty: request authentication still proceeds (unchanged
// legacy behaviour), while delegated MCP credential use fails closed on the
// missing canonical owner.
func (r *Runtime) resolveBearerCanonicalUserID(ctx context.Context, subject, email string) string {
	resolver := r.canonicalResolver()
	if resolver == nil || strings.TrimSpace(subject) == "" {
		return ""
	}
	provider := "jwt"
	if r != nil && r.ext != nil {
		provider = r.ext.oauthProviderName()
	}
	identity := VerifiedWorkspaceIdentity{Provider: provider, Subject: subject, Email: email}
	id, err := resolver.ResolveCanonicalWorkspaceUser(ctx, identity)
	if err == nil {
		return id
	}
	// Legacy JWT users may be recorded under provider "jwt" rather than the
	// configured workspace OAuth provider; apply the same resolver with that
	// alias before giving up.
	if provider != "jwt" {
		if id, err := resolver.ResolveCanonicalWorkspaceUser(ctx, VerifiedWorkspaceIdentity{Provider: "jwt", Subject: subject, Email: email}); err == nil {
			return id
		}
	}
	return ""
}

func (r *Runtime) ensureSessionCanonicalUserID(ctx context.Context, sess *Session) {
	if r == nil || r.ext == nil || sess == nil || strings.TrimSpace(sess.UserID) != "" {
		return
	}
	r.ext.canonicalizeSessionUser(ctx, sess)
	if strings.TrimSpace(sess.UserID) == "" || r.sessions == nil {
		return
	}
	r.sessions.PutAsync(ctx, sess)
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
	if r.cfg.Local != nil && r.cfg.Local.Enabled {
		return false
	}
	mode := strings.ToLower(strings.TrimSpace(r.cfg.OAuth.Mode))
	return mode == "bff" || mode == "mixed"
}

func (r *Runtime) requiresOAuthTokensForSession(sess *Session) bool {
	if r == nil {
		return false
	}
	provider := "oauth"
	if r.ext != nil {
		provider = r.ext.oauthProviderName()
	}
	return requiresOAuthTokensForSession(r.cfg, provider, sess)
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
	// Auto-bootstrap local dev sessions should not block auth endpoints on
	// durable session persistence. Keep the session immediately usable in
	// memory, then let persistence happen out-of-band.
	r.sessions.PutAsync(req.Context(), session)
	writeSessionCookie(w, r.cfg, r.sessions, session.ID)
	return &runtimeAuthUser{Subject: username, Provider: "local"}
}

func runtimeUserInfo(ctx context.Context) *UserInfo {
	return fromInternalUserInfo(iauth.User(ctx))
}
