package auth

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	scyauth "github.com/viant/scy/auth"
	"golang.org/x/oauth2"
)

type authExtension struct {
	cfg        *Config
	sessions   *Manager
	jwtSignKey string
	tokenStore TokenStore
	users      UserService
}

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
	mux.HandleFunc("GET /v1/api/auth/oauth/config", a.handleOAuthConfig())
	mux.HandleFunc("POST /v1/api/auth/oauth/initiate", a.handleOAuthInitiate())
	mux.HandleFunc("GET /v1/api/auth/oauth/callback", a.handleOAuthCallback())
	mux.HandleFunc("POST /v1/api/auth/oauth/callback", a.handleOAuthCallback())
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

func (a *authExtension) persistOAuthToken(ctx context.Context, source, username, email, subject, provider, accessToken, idToken, refreshToken string, expiresAt time.Time) {
	if a == nil {
		return
	}
	storeUser := strings.TrimSpace(firstNonEmpty(subject, username))
	if provider == "" {
		provider = a.oauthProviderName()
	}
	if a.users != nil {
		log.Printf("[auth-oauth] source=%s ensure user start subject=%q username=%q email=%q provider=%q",
			source,
			strings.TrimSpace(subject),
			strings.TrimSpace(username),
			strings.TrimSpace(email),
			strings.TrimSpace(provider),
		)
		userID, err := a.users.UpsertWithProvider(ctx, strings.TrimSpace(username), strings.TrimSpace(username), strings.TrimSpace(email), strings.TrimSpace(provider), strings.TrimSpace(subject))
		if err != nil {
			log.Printf("[auth-oauth] source=%s ensure user failed subject=%q username=%q provider=%q err=%v",
				source,
				strings.TrimSpace(subject),
				strings.TrimSpace(username),
				strings.TrimSpace(provider),
				err,
			)
			return
		}
		if strings.TrimSpace(userID) != "" {
			storeUser = strings.TrimSpace(userID)
		}
		log.Printf("[auth-oauth] source=%s ensure user ok subject=%q username=%q user_id=%q provider=%q",
			source,
			strings.TrimSpace(subject),
			strings.TrimSpace(username),
			storeUser,
			strings.TrimSpace(provider),
		)
	}
	log.Printf("[auth-oauth] source=%s persist token start subject=%q username=%q store_user=%q provider=%q has_access=%t has_refresh=%t has_id=%t",
		source,
		strings.TrimSpace(subject),
		strings.TrimSpace(username),
		storeUser,
		strings.TrimSpace(provider),
		strings.TrimSpace(accessToken) != "",
		strings.TrimSpace(refreshToken) != "",
		strings.TrimSpace(idToken) != "",
	)
	if a.tokenStore == nil {
		log.Printf("[auth-oauth] source=%s persist token skipped store_user=%q provider=%q reason=%q",
			source,
			storeUser,
			strings.TrimSpace(provider),
			"token store unavailable",
		)
		return
	}
	if err := a.tokenStore.Put(ctx, &OAuthToken{
		Username:     storeUser,
		Provider:     provider,
		AccessToken:  strings.TrimSpace(accessToken),
		IDToken:      strings.TrimSpace(idToken),
		RefreshToken: strings.TrimSpace(refreshToken),
		ExpiresAt:    expiresAt,
	}); err != nil {
		log.Printf("[auth-oauth] source=%s persist token failed store_user=%q provider=%q err=%v",
			source,
			storeUser,
			strings.TrimSpace(provider),
			err,
		)
		return
	}
	log.Printf("[auth-oauth] source=%s persist token ok store_user=%q provider=%q",
		source,
		storeUser,
		strings.TrimSpace(provider),
	)
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
	mode := strings.ToLower(strings.TrimSpace(a.cfg.OAuth.Mode))
	return mode == "bff" || mode == "mixed"
}

func (a *authExtension) ensureSessionOAuthTokens(ctx context.Context, sess *Session) bool {
	if sess == nil {
		return false
	}
	if sess.Tokens != nil && (strings.TrimSpace(sess.Tokens.AccessToken) != "" || strings.TrimSpace(sess.Tokens.IDToken) != "") {
		return true
	}
	if a == nil || a.tokenStore == nil {
		return false
	}
	lookupID := strings.TrimSpace(firstNonEmpty(sess.Subject, sess.Username))
	if a.users != nil {
		if user, err := a.users.GetByUsername(ctx, strings.TrimSpace(firstNonEmpty(sess.Username, sess.Subject))); err == nil && user != nil && strings.TrimSpace(user.ID) != "" {
			lookupID = strings.TrimSpace(user.ID)
		}
	}
	if lookupID == "" {
		return false
	}
	provider := a.oauthProviderName()
	dbTok, err := a.tokenStore.Get(ctx, lookupID, provider)
	if err != nil || dbTok == nil {
		return false
	}
	if dbTok.ExpiresAt.IsZero() || !dbTok.ExpiresAt.After(time.Now()) {
		return false
	}
	sess.Tokens = &scyauth.Token{
		Token: oauth2.Token{
			AccessToken:  dbTok.AccessToken,
			RefreshToken: dbTok.RefreshToken,
			Expiry:       dbTok.ExpiresAt,
		},
		IDToken: dbTok.IDToken,
	}
	sess.Provider = provider
	a.sessions.Put(ctx, sess)
	return true
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
