package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/viant/agently-core/internal/authlog"
	"github.com/viant/agently-core/internal/logx"
)

type authExtension struct {
	cfg        *Config
	sessions   *Manager
	jwtSignKey string
	tokenStore TokenStore
	users      UserService
	oauthOOB   oobOAuthAuthorizer
}

var authPersistMu sync.Mutex

const authPersistTimeout = 15 * time.Second
const authDisplayLookupTimeout = 750 * time.Millisecond

func newAuthExtension(cfg *Config, sessions *Manager, jwtSignKey string, tokenStore TokenStore, users UserService) *authExtension {
	if cfg == nil || sessions == nil {
		return nil
	}
	return &authExtension{
		cfg:        cfg,
		sessions:   sessions,
		jwtSignKey: strings.TrimSpace(jwtSignKey),
		tokenStore: tokenStore,
		users:      users,
	}
}

func (a *authExtension) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/api/auth/me", a.handleMe())
	mux.HandleFunc("POST /v1/api/auth/local/login", a.handleLocalLogin())
	mux.HandleFunc("POST /v1/api/auth/logout", a.handleLogout())
	mux.HandleFunc("GET /v1/api/auth/providers", a.handleProviders())
	mux.HandleFunc("POST /v1/api/auth/session", a.handleCreateSession())
	mux.HandleFunc("POST /v1/api/auth/session/attach", a.handleAttachSession())
	mux.HandleFunc("GET /v1/api/auth/oauth/config", a.handleOAuthConfig())
	mux.HandleFunc("POST /v1/api/auth/oauth/initiate", a.handleOAuthInitiate())
	mux.HandleFunc("POST /v1/api/auth/oauth/mobile/initiate", a.handleOAuthMobileInitiate())
	mux.HandleFunc("GET /v1/api/auth/oauth/callback", a.handleOAuthCallback())
	mux.HandleFunc("POST /v1/api/auth/oauth/callback", a.handleOAuthCallback())
	mux.HandleFunc("POST /v1/api/auth/oauth/mobile/callback", a.handleOAuthMobileCallback())
	mux.HandleFunc("POST /v1/api/auth/oob", a.handleOAuthOOB())
	mux.HandleFunc("POST /v1/api/auth/idp/delegate", a.handleIDPDelegate())
	mux.HandleFunc("GET /v1/api/auth/idp/login", a.handleIDPLogin())
	mux.HandleFunc("POST /v1/api/auth/jwt/keypair", a.handleJWTKeyPair())
	mux.HandleFunc("POST /v1/api/auth/jwt/mint", a.handleJWTMint())
}

func writeSessionCookie(w http.ResponseWriter, cfg *Config, sessions *Manager, sessionID string) {
	if cfg == nil || strings.TrimSpace(cfg.CookieName) == "" || sessions == nil {
		return
	}
	http.SetCookie(w, &http.Cookie{Name: cfg.CookieName, Value: sessionID, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: int(sessionsTTLSeconds(cfg))})
}

func sessionsTTLSeconds(cfg *Config) int64 {
	hours := cfg.SessionTTLHours
	if hours <= 0 {
		hours = 24 * 7
	}
	return int64(time.Duration(hours) * time.Hour / time.Second)
}

func (a *authExtension) oauthProviderName() string {
	if a.cfg == nil || a.cfg.OAuth == nil {
		return "oauth"
	}
	if name := strings.TrimSpace(a.cfg.OAuth.Name); name != "" {
		return name
	}
	return "oauth"
}

func (a *authExtension) persistOAuthToken(ctx context.Context, source, username, email, subject, provider, accessToken, idToken, refreshToken string, expiresAt time.Time) error {
	if a == nil {
		return nil
	}
	authPersistMu.Lock()
	defer authPersistMu.Unlock()
	persistCtx, cancel := durableAuthPersistContext(ctx)
	defer cancel()
	storeUser := strings.TrimSpace(firstNonEmpty(subject, username))
	if provider == "" {
		provider = a.oauthProviderName()
	}
	if a.users != nil {
		logx.Debugf("auth-token", "callback owner resolution start source=%q provider=%q", strings.TrimSpace(source), strings.TrimSpace(provider))
		started := time.Now()
		userID, err := a.users.UpsertWithProvider(persistCtx, strings.TrimSpace(username), strings.TrimSpace(username), strings.TrimSpace(email), strings.TrimSpace(provider), strings.TrimSpace(subject))
		if err != nil {
			authlog.Log(ctx, authlog.Event{
				Op:             "callback_owner_persist",
				Provider:       provider,
				Classification: "owner_resolution",
				Action:         "preserve",
				Elapsed:        time.Since(started),
			})
			return fmt.Errorf("oauth token owner persistence failed")
		}
		if strings.TrimSpace(userID) == "" {
			authlog.Log(ctx, authlog.Event{
				Op:             "callback_owner_persist",
				Provider:       provider,
				Classification: "owner_resolution",
				Action:         "preserve",
				Elapsed:        time.Since(started),
			})
			return fmt.Errorf("oauth token owner persistence returned no user")
		}
		storeUser = strings.TrimSpace(userID)
		logx.Debugf("auth-token", "callback owner resolution ok source=%q user_id=%q provider=%q",
			strings.TrimSpace(source), storeUser, strings.TrimSpace(provider))
	}
	logx.Debugf("auth-token", "callback persist start source=%q user_id=%q provider=%q",
		strings.TrimSpace(source), storeUser, strings.TrimSpace(provider))
	if a.tokenStore == nil {
		logx.Debugf("auth-token", "callback persist skipped user_id=%q provider=%q", storeUser, strings.TrimSpace(provider))
		return nil
	}
	started := time.Now()
	if err := a.tokenStore.Put(persistCtx, &OAuthToken{
		Username:     storeUser,
		Provider:     provider,
		AccessToken:  strings.TrimSpace(accessToken),
		IDToken:      strings.TrimSpace(idToken),
		RefreshToken: strings.TrimSpace(refreshToken),
		ExpiresAt:    expiresAt,
	}); err != nil {
		authlog.Log(ctx, authlog.Event{
			Op:             "callback_persist",
			UserID:         storeUser,
			Provider:       provider,
			Classification: "persistence",
			Action:         "preserve",
			Elapsed:        time.Since(started),
		})
		return fmt.Errorf("oauth token persistence failed")
	}
	logx.Debugf("auth-token", "callback persist ok source=%q user_id=%q provider=%q",
		strings.TrimSpace(source), storeUser, strings.TrimSpace(provider))
	return nil
}

func (a *authExtension) scheduleOAuthTokenPersist(ctx context.Context, source, username, email, subject, provider, accessToken, idToken, refreshToken string, expiresAt time.Time) {
	if a == nil {
		return
	}
	go a.persistOAuthToken(ctx, source, username, email, subject, provider, accessToken, idToken, refreshToken, expiresAt)
}

func durableAuthPersistContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		return context.WithTimeout(context.Background(), authPersistTimeout)
	}
	return context.WithTimeout(context.Background(), authPersistTimeout)
}

func durableAuthLookupContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), authDisplayLookupTimeout)
}

func (a *authExtension) canonicalizeSessionUser(ctx context.Context, sess *Session) {
	if a == nil || sess == nil || a.users == nil {
		return
	}
	provider := strings.TrimSpace(firstNonEmpty(sess.Provider, a.oauthProviderName()))
	subject := strings.TrimSpace(sess.Subject)
	if provider != "" && subject != "" {
		lookupCtx, cancel := durableAuthLookupContext(ctx)
		defer cancel()
		if user, err := a.users.GetBySubjectAndProvider(lookupCtx, subject, provider); err == nil && user != nil && strings.TrimSpace(user.ID) != "" {
			sess.UserID = strings.TrimSpace(user.ID)
			if strings.TrimSpace(sess.Username) == "" {
				sess.Username = strings.TrimSpace(firstNonEmpty(user.Username, user.DisplayName, sess.Username))
			}
			if strings.TrimSpace(sess.Email) == "" {
				sess.Email = strings.TrimSpace(firstNonEmpty(user.Email, sess.Email))
			}
			return
		}
	}
	if strings.TrimSpace(sess.UserID) != "" {
		return
	}
	username := strings.TrimSpace(sess.Username)
	if username == "" {
		return
	}
	lookupCtx, cancel := durableAuthLookupContext(ctx)
	defer cancel()
	if user, err := a.users.GetByUsername(lookupCtx, username); err == nil && user != nil && strings.TrimSpace(user.ID) != "" {
		sess.UserID = strings.TrimSpace(user.ID)
		if strings.TrimSpace(sess.Email) == "" {
			sess.Email = strings.TrimSpace(firstNonEmpty(user.Email, sess.Email))
		}
	}
}

func (a *authExtension) currentSession(r *http.Request) *Session {
	if a == nil || a.sessions == nil || a.cfg == nil {
		return nil
	}
	cookieName := strings.TrimSpace(a.cfg.CookieName)
	if cookieName == "" {
		return nil
	}
	c, err := r.Cookie(cookieName)
	if err != nil {
		return nil
	}
	id := strings.TrimSpace(c.Value)
	if id == "" {
		return nil
	}
	return a.sessions.Get(r.Context(), id)
}

func (a *authExtension) requiresOAuthTokens() bool {
	if a == nil || a.cfg == nil || a.cfg.OAuth == nil {
		return false
	}
	if a.cfg.Local != nil && a.cfg.Local.Enabled {
		return false
	}
	mode := strings.ToLower(strings.TrimSpace(a.cfg.OAuth.Mode))
	return mode == "bff" || mode == "mixed"
}

func requiresOAuthTokensForSession(cfg *Config, oauthProvider string, sess *Session) bool {
	if cfg == nil || cfg.OAuth == nil {
		return false
	}
	mode := strings.ToLower(strings.TrimSpace(cfg.OAuth.Mode))
	if mode != "bff" && mode != "mixed" {
		return false
	}
	if cfg.Local == nil || !cfg.Local.Enabled {
		return true
	}
	provider := ""
	if sess != nil {
		provider = strings.TrimSpace(sess.Provider)
	}
	if provider == "" || strings.EqualFold(provider, "local") {
		return false
	}
	return strings.EqualFold(provider, strings.TrimSpace(firstNonEmpty(oauthProvider, "oauth")))
}

func (a *authExtension) requiresOAuthTokensForSession(sess *Session) bool {
	if a == nil {
		return false
	}
	return requiresOAuthTokensForSession(a.cfg, a.oauthProviderName(), sess)
}

func (a *authExtension) sessionOAuthTokenAvailability(ctx context.Context, sess *Session) tokenAvailabilityResult {
	if a == nil || sess == nil {
		return preserveOAuthSession()
	}
	var client *OAuthClient
	if a.cfg != nil && a.cfg.OAuth != nil {
		client = a.cfg.OAuth.Client
	}
	provider := strings.TrimSpace(firstNonEmpty(sess.Provider, a.oauthProviderName()))
	result := resolveSessionOAuthToken(ctx, a.users, a.tokenStore, provider, client, sess)
	if result.state != tokenAvailable || result.token == nil || result.token == sess.Tokens {
		return result
	}
	sess.Tokens = result.token
	sess.Provider = provider
	if a.sessions != nil {
		a.sessions.Put(ctx, sess)
	}
	return result
}

// ensureSessionOAuthTokens is retained as a compatibility shim for existing
// package callers. Request handling uses the tri-state result above.
func (a *authExtension) ensureSessionOAuthTokens(ctx context.Context, sess *Session) bool {
	return a.sessionOAuthTokenAvailability(ctx, sess).state == tokenAvailable
}

func runtimeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func runtimeError(w http.ResponseWriter, status int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":  "error",
		"message": err.Error(),
	})
}
