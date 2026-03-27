package auth

import (
	"context"
	"encoding/json"
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
}

func newAuthExtension(cfg *Config, sessions *Manager, jwtSignKey string, tokenStore TokenStore) *authExtension {
	if cfg == nil || sessions == nil {
		return nil
	}
	return &authExtension{
		cfg:        cfg,
		sessions:   sessions,
		jwtSignKey: strings.TrimSpace(jwtSignKey),
		tokenStore: tokenStore,
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
	username := strings.TrimSpace(firstNonEmpty(sess.Subject, sess.Username))
	if username == "" {
		return false
	}
	provider := a.oauthProviderName()
	dbTok, err := a.tokenStore.Get(ctx, username, provider)
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
