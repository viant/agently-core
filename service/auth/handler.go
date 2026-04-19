package auth

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	iauth "github.com/viant/agently-core/internal/auth"
	token "github.com/viant/agently-core/internal/auth/token"
)

// Handler serves auth-related HTTP endpoints. It is designed to be mounted
// under /v1/api/auth/* on the application router.
type Handler struct {
	cfg           *Config
	sessions      *Manager
	users         UserService    // optional
	tokens        TokenStore     // optional
	tokenProvider token.Provider // optional — shared token lifecycle manager
}

// NewHandler creates an auth HTTP handler.
func NewHandler(cfg *Config, sessions *Manager, opts ...HandlerOption) *Handler {
	h := &Handler{cfg: cfg, sessions: sessions}
	for _, o := range opts {
		o(h)
	}
	return h
}

// HandlerOption customises the auth Handler.
type HandlerOption func(*Handler)

// WithUserService injects a user service.
func WithUserService(us UserService) HandlerOption {
	return func(h *Handler) { h.users = us }
}

// WithTokenStore injects an OAuth token store.
func WithTokenStore(ts TokenStore) HandlerOption {
	return func(h *Handler) { h.tokens = ts }
}

// WithTokenProvider injects a shared token lifecycle manager.
func WithTokenProvider(tp token.Provider) HandlerOption {
	return func(h *Handler) { h.tokenProvider = tp }
}

// Register mounts auth routes on the given mux.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/api/auth/local/login", h.handleLocalLogin())
	mux.HandleFunc("GET /v1/api/auth/me", h.handleMe())
	mux.HandleFunc("POST /v1/api/auth/logout", h.handleLogout())
	mux.HandleFunc("GET /v1/api/auth/providers", h.handleProviders())
	mux.HandleFunc("POST /v1/api/auth/oauth/initiate", h.handleOAuthInitiate())
	mux.HandleFunc("GET /v1/api/auth/oauth/callback", h.handleOAuthCallback())
	mux.HandleFunc("POST /v1/api/auth/oauth/callback", h.handleOAuthCallback())
	mux.HandleFunc("POST /v1/api/auth/oob", h.handleOOB())
	mux.HandleFunc("GET /v1/api/auth/oauth/config", h.handleOAuthConfig())
	mux.HandleFunc("POST /v1/api/auth/session", h.handleCreateSession())
}

func (h *Handler) handleLocalLogin() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if h.cfg == nil || !h.cfg.IsLocalAuth() {
			httpError(w, http.StatusForbidden, fmt.Errorf("local auth not enabled"))
			return
		}
		var body struct {
			Username string `json:"username"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		username := strings.TrimSpace(body.Username)
		if username == "" {
			httpError(w, http.StatusBadRequest, fmt.Errorf("username is required"))
			return
		}
		sess := &Session{
			ID:        uuid.New().String(),
			Username:  username,
			Subject:   username, // local auth has no JWT sub; use username as stable identity
			Provider:  "local",
			CreatedAt: time.Now(),
		}
		h.sessions.Put(r.Context(), sess)

		if h.users != nil {
			if err := h.users.Upsert(r.Context(), &User{Username: username}); err != nil {
				httpError(w, http.StatusInternalServerError, fmt.Errorf("failed to upsert user: %w", err))
				return
			}
		}

		if h.cfg.CookieName != "" {
			http.SetCookie(w, &http.Cookie{
				Name:     h.cfg.CookieName,
				Value:    sess.ID,
				Path:     "/",
				HttpOnly: true,
				SameSite: http.SameSiteLaxMode,
				MaxAge:   int(h.sessions.ttl.Seconds()),
			})
		}
		httpJSON(w, http.StatusOK, map[string]string{"sessionId": sess.ID, "username": username})
	}
}

func (h *Handler) handleMe() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ui := iauth.User(r.Context())
		if ui == nil {
			httpError(w, http.StatusUnauthorized, fmt.Errorf("not authenticated"))
			return
		}
		resp := map[string]interface{}{
			"subject": ui.Subject,
			"email":   ui.Email,
		}
		if h.users != nil {
			if u, err := h.users.GetByUsername(r.Context(), ui.Subject); err == nil && u != nil {
				resp["displayName"] = u.DisplayName
				resp["preferences"] = u.Preferences
			}
		}
		httpJSON(w, http.StatusOK, resp)
	}
}

func (h *Handler) handleLogout() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Invalidate tokens in shared provider.
		if h.tokenProvider != nil {
			userID := iauth.EffectiveUserID(r.Context())
			if userID != "" {
				if err := h.tokenProvider.Invalidate(r.Context(), token.Key{Subject: userID, Provider: effectiveTokenProvider(h.cfg)}); err != nil {
					httpError(w, http.StatusInternalServerError, fmt.Errorf("failed to invalidate token: %w", err))
					return
				}
			}
		}
		if h.cfg.CookieName != "" {
			if c, err := r.Cookie(h.cfg.CookieName); err == nil {
				h.sessions.Delete(r.Context(), c.Value)
			}
			http.SetCookie(w, &http.Cookie{
				Name:     h.cfg.CookieName,
				Value:    "",
				Path:     "/",
				HttpOnly: true,
				MaxAge:   -1,
			})
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (h *Handler) handleProviders() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		providers := []map[string]interface{}{}
		if h.cfg.Local != nil && h.cfg.Local.Enabled {
			providers = append(providers, map[string]interface{}{
				"type": "local",
				"name": "local",
			})
		}
		if h.cfg.OAuth != nil {
			p := map[string]interface{}{
				"type": "oauth",
				"name": h.cfg.OAuth.Name,
				"mode": h.cfg.OAuth.Mode,
			}
			if h.cfg.OAuth.Label != "" {
				p["label"] = h.cfg.OAuth.Label
			}
			providers = append(providers, p)
		}
		if h.cfg.JWT != nil && h.cfg.JWT.Enabled {
			providers = append(providers, map[string]interface{}{
				"type": "jwt",
				"name": "jwt",
			})
		}
		httpJSON(w, http.StatusOK, map[string]interface{}{"providers": providers})
	}
}

func (h *Handler) handleOAuthInitiate() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if h.cfg.OAuth == nil || h.cfg.OAuth.Client == nil {
			httpError(w, http.StatusBadRequest, fmt.Errorf("oauth not configured"))
			return
		}
		client := h.cfg.OAuth.Client
		state := uuid.New().String()
		// Build authorization URL
		scopes := strings.Join(client.Scopes, " ")
		authURL := fmt.Sprintf("%s?client_id=%s&redirect_uri=%s&response_type=code&scope=%s&state=%s",
			client.ConfigURL, client.ClientID, client.RedirectURI, scopes, state)
		httpJSON(w, http.StatusOK, map[string]string{"authUrl": authURL, "state": state})
	}
}

func (h *Handler) handleOAuthCallback() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// OAuth callback stub — actual token exchange requires provider-specific
		// logic which the host application wires via TokenStore.
		httpJSON(w, http.StatusOK, map[string]string{"status": "callback_received"})
	}
}

func (h *Handler) handleOOB() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			AccessToken  string `json:"accessToken"`
			IDToken      string `json:"idToken,omitempty"`
			RefreshToken string `json:"refreshToken,omitempty"`
			Username     string `json:"username,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		if strings.TrimSpace(body.AccessToken) == "" {
			httpError(w, http.StatusBadRequest, fmt.Errorf("accessToken is required"))
			return
		}
		sess := &Session{
			ID:        uuid.New().String(),
			Username:  body.Username,
			Provider:  firstNonEmpty(strings.TrimSpace(h.cfg.OAuth.Name), "oauth"),
			CreatedAt: time.Now(),
		}
		sess.Tokens = newTokenBundle(body.AccessToken, body.IDToken, body.RefreshToken)
		h.sessions.Put(r.Context(), sess)

		// Store tokens in shared provider for downstream use.
		if h.tokenProvider != nil && sess.EffectiveUserID() != "" && sess.Tokens != nil {
			if err := h.tokenProvider.Store(r.Context(), token.Key{
				Subject:  sess.EffectiveUserID(),
				Provider: effectiveTokenProvider(h.cfg),
			}, sess.Tokens); err != nil {
				httpError(w, http.StatusInternalServerError, fmt.Errorf("failed to store token: %w", err))
				return
			}
		}

		if h.cfg.CookieName != "" {
			http.SetCookie(w, &http.Cookie{
				Name:     h.cfg.CookieName,
				Value:    sess.ID,
				Path:     "/",
				HttpOnly: true,
				SameSite: http.SameSiteLaxMode,
				MaxAge:   int(h.sessions.ttl.Seconds()),
			})
		}
		httpJSON(w, http.StatusOK, map[string]string{"sessionId": sess.ID})
	}
}

func (h *Handler) handleOAuthConfig() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if h.cfg.OAuth == nil || h.cfg.OAuth.Client == nil {
			httpJSON(w, http.StatusOK, map[string]interface{}{})
			return
		}
		c := h.cfg.OAuth.Client
		resp := map[string]interface{}{
			"mode":            h.cfg.OAuth.Mode,
			"configURL":       c.ConfigURL,
			"clientId":        c.ClientID,
			"usePopupLogin":   h.cfg.OAuth.UsePopupLogin,
			"redirectSameTab": !h.cfg.OAuth.UsePopupLogin,
			"scopes":          c.Scopes,
		}
		if c.DiscoveryURL != "" {
			resp["discoveryUrl"] = c.DiscoveryURL
		}
		if c.RedirectURI != "" {
			resp["redirectUri"] = c.RedirectURI
		}
		httpJSON(w, http.StatusOK, resp)
	}
}

func (h *Handler) handleCreateSession() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Username     string `json:"username"`
			AccessToken  string `json:"accessToken,omitempty"`
			IDToken      string `json:"idToken,omitempty"`
			RefreshToken string `json:"refreshToken,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		username := strings.TrimSpace(body.Username)
		if username == "" {
			username = "anonymous:" + uuid.New().String()
		}
		sess := &Session{
			ID:        uuid.New().String(),
			Username:  username,
			Provider:  firstNonEmpty(strings.TrimSpace(h.cfg.OAuth.Name), "oauth"),
			CreatedAt: time.Now(),
		}
		if body.AccessToken != "" {
			sess.Tokens = newTokenBundle(body.AccessToken, body.IDToken, body.RefreshToken)
		}
		h.sessions.Put(r.Context(), sess)

		// Store tokens in shared provider for downstream use.
		if h.tokenProvider != nil && sess.EffectiveUserID() != "" && sess.Tokens != nil {
			if err := h.tokenProvider.Store(r.Context(), token.Key{
				Subject:  sess.EffectiveUserID(),
				Provider: effectiveTokenProvider(h.cfg),
			}, sess.Tokens); err != nil {
				httpError(w, http.StatusInternalServerError, fmt.Errorf("failed to store token: %w", err))
				return
			}
		}
		if h.cfg != nil && h.cfg.CookieName != "" {
			http.SetCookie(w, &http.Cookie{
				Name:     h.cfg.CookieName,
				Value:    sess.ID,
				Path:     "/",
				HttpOnly: true,
				SameSite: http.SameSiteLaxMode,
				MaxAge:   int(h.sessions.ttl.Seconds()),
			})
		}
		httpJSON(w, http.StatusOK, map[string]string{"sessionId": sess.ID})
	}
}

func httpJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func httpError(w http.ResponseWriter, code int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}
