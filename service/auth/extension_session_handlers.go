package auth

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	scyauth "github.com/viant/scy/auth"
	"golang.org/x/oauth2"
)

func (a *authExtension) handleMe() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess := a.currentSession(r)
		if sess == nil {
			if user := RuntimeUserFromContext(r.Context()); user != nil {
				displayName := strings.TrimSpace(user.Subject)
				if a.users != nil {
					provider := a.oauthProviderName()
					if resolved, err := a.users.GetBySubjectAndProvider(r.Context(), strings.TrimSpace(user.Subject), provider); err == nil && resolved != nil {
						if v := strings.TrimSpace(resolved.DisplayName); v != "" {
							displayName = v
						} else if v := strings.TrimSpace(resolved.Username); v != "" {
							displayName = v
						}
					}
				}
				runtimeJSON(w, http.StatusOK, map[string]any{
					"subject":     strings.TrimSpace(user.Subject),
					"username":    strings.TrimSpace(user.Subject),
					"email":       strings.TrimSpace(user.Email),
					"displayName": displayName,
					"provider":    "jwt",
				})
				return
			}
			runtimeError(w, http.StatusUnauthorized, fmt.Errorf("not authenticated"))
			return
		}
		if a.requiresOAuthTokens() && !a.ensureSessionOAuthTokens(r.Context(), sess) {
			if cookieName := strings.TrimSpace(a.cfg.CookieName); cookieName != "" {
				if c, err := r.Cookie(cookieName); err == nil && strings.TrimSpace(c.Value) != "" {
					a.sessions.Delete(r.Context(), strings.TrimSpace(c.Value))
				}
				http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", HttpOnly: true, MaxAge: -1})
			}
			runtimeError(w, http.StatusUnauthorized, fmt.Errorf("oauth session is missing a valid token"))
			return
		}
		displayName := strings.TrimSpace(sess.Username)
		if a.users != nil {
			provider := strings.TrimSpace(firstNonEmpty(sess.Provider, a.oauthProviderName()))
			if resolved, err := a.users.GetBySubjectAndProvider(r.Context(), strings.TrimSpace(sess.Subject), provider); err == nil && resolved != nil {
				if v := strings.TrimSpace(resolved.DisplayName); v != "" {
					displayName = v
				} else if v := strings.TrimSpace(resolved.Username); v != "" {
					displayName = v
				}
			}
		}
		runtimeJSON(w, http.StatusOK, map[string]any{
			"subject":     strings.TrimSpace(sess.Subject),
			"username":    strings.TrimSpace(sess.Username),
			"email":       strings.TrimSpace(sess.Email),
			"displayName": displayName,
			"provider":    "session",
		})
	}
}

func (a *authExtension) handleLocalLogin() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if a.cfg == nil || a.cfg.Local == nil || !a.cfg.Local.Enabled {
			runtimeError(w, http.StatusForbidden, fmt.Errorf("local auth is not enabled"))
			return
		}
		var in struct {
			Username string `json:"username"`
			Name     string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			runtimeError(w, http.StatusBadRequest, err)
			return
		}
		username := strings.TrimSpace(in.Username)
		if username == "" {
			username = strings.TrimSpace(in.Name)
		}
		if username == "" {
			runtimeError(w, http.StatusBadRequest, fmt.Errorf("username is required"))
			return
		}
		sess := &Session{ID: uuid.New().String(), Username: username, Subject: username, Provider: "local", CreatedAt: time.Now()}
		a.sessions.Put(r.Context(), sess)
		if a.users != nil {
			_ = a.users.Upsert(r.Context(), &User{Username: username, DisplayName: username, Provider: "local"})
		}
		writeSessionCookie(w, a.cfg, a.sessions, sess.ID)
		runtimeJSON(w, http.StatusOK, map[string]any{"sessionId": sess.ID, "username": username, "provider": "local"})
	}
}

func (a *authExtension) handleLogout() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if cookieName := strings.TrimSpace(a.cfg.CookieName); cookieName != "" {
			if c, err := r.Cookie(cookieName); err == nil && strings.TrimSpace(c.Value) != "" {
				a.sessions.Delete(r.Context(), c.Value)
			}
			http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", HttpOnly: true, MaxAge: -1})
		}
		runtimeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
	}
}

func (a *authExtension) handleProviders() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		providers := make([]map[string]any, 0, 3)
		if a.cfg != nil && a.cfg.Local != nil && a.cfg.Local.Enabled {
			item := map[string]any{"name": "local", "label": "Local User", "type": "local"}
			if strings.TrimSpace(a.cfg.DefaultUsername) != "" {
				item["defaultUsername"] = strings.TrimSpace(a.cfg.DefaultUsername)
			}
			providers = append(providers, item)
		}
		if a.cfg != nil && a.cfg.OAuth != nil && a.cfg.OAuth.Client != nil {
			mode := strings.ToLower(strings.TrimSpace(a.cfg.OAuth.Mode))
			if mode == "bff" || mode == "mixed" {
				providers = append(providers, map[string]any{"name": a.oauthProviderName(), "label": firstNonEmpty(a.cfg.OAuth.Label, "OAuth2"), "type": "bff"})
			}
			if mode == "spa" || mode == "bearer" || mode == "oidc" || mode == "mixed" {
				providers = append(providers, map[string]any{
					"name":         a.oauthProviderName(),
					"label":        firstNonEmpty(a.cfg.OAuth.Label, "OIDC"),
					"type":         "oidc",
					"clientID":     strings.TrimSpace(a.cfg.OAuth.Client.ClientID),
					"discoveryURL": strings.TrimSpace(a.cfg.OAuth.Client.DiscoveryURL),
					"redirectURI":  strings.TrimSpace(a.cfg.OAuth.Client.RedirectURI),
					"scopes":       append([]string(nil), a.cfg.OAuth.Client.Scopes...),
				})
			}
		}
		if a.cfg != nil && a.cfg.JWT != nil && a.cfg.JWT.Enabled {
			providers = append(providers, map[string]any{"name": "jwt", "label": "JWT", "type": "jwt"})
		}
		runtimeJSON(w, http.StatusOK, providers)
	}
}

func (a *authExtension) handleOAuthConfig() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if a.cfg == nil || a.cfg.OAuth == nil || a.cfg.OAuth.Client == nil {
			runtimeJSON(w, http.StatusOK, map[string]any{})
			return
		}
		runtimeJSON(w, http.StatusOK, map[string]any{
			"mode":          strings.TrimSpace(a.cfg.OAuth.Mode),
			"configURL":     strings.TrimSpace(a.cfg.OAuth.Client.ConfigURL),
			"clientID":      strings.TrimSpace(a.cfg.OAuth.Client.ClientID),
			"discoveryURL":  strings.TrimSpace(a.cfg.OAuth.Client.DiscoveryURL),
			"redirectURI":   strings.TrimSpace(a.cfg.OAuth.Client.RedirectURI),
			"usePopupLogin": a.cfg.OAuth.UsePopupLogin,
			"scopes":        append([]string(nil), a.cfg.OAuth.Client.Scopes...),
		})
	}
}

func (a *authExtension) handleCreateSession() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			runtimeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
			return
		}
		var in struct {
			Username     string `json:"username"`
			AccessToken  string `json:"accessToken,omitempty"`
			IDToken      string `json:"idToken,omitempty"`
			RefreshToken string `json:"refreshToken,omitempty"`
			ExpiresAt    string `json:"expiresAt,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			runtimeError(w, http.StatusBadRequest, err)
			return
		}
		bearerToken := bearerTokenFromRequest(r)
		bearerBootstrap := strings.TrimSpace(in.IDToken) == "" && strings.TrimSpace(in.AccessToken) == "" && strings.TrimSpace(in.RefreshToken) == "" && bearerToken != ""
		if strings.TrimSpace(in.IDToken) == "" && strings.TrimSpace(in.AccessToken) == "" && bearerToken != "" {
			in.IDToken = bearerToken
			in.AccessToken = bearerToken
		}
		username := strings.TrimSpace(in.Username)
		subject := ""
		email := ""
		if username == "" {
			username, subject, email, _ = identityFromTokenStrings(strings.TrimSpace(in.IDToken), strings.TrimSpace(in.AccessToken))
		} else {
			_, subject, email, _ = identityFromTokenStrings(strings.TrimSpace(in.IDToken), strings.TrimSpace(in.AccessToken))
		}
		if username == "" {
			username = "user"
		}
		if subject == "" {
			subject = username
		}
		sess := &Session{
			ID:        uuid.New().String(),
			Username:  username,
			Email:     email,
			Subject:   subject,
			Provider:  a.oauthProviderName(),
			CreatedAt: time.Now(),
		}
		if strings.TrimSpace(in.AccessToken) != "" || strings.TrimSpace(in.IDToken) != "" || strings.TrimSpace(in.RefreshToken) != "" {
			sess.Tokens = &scyauth.Token{
				Token: oauth2.Token{
					AccessToken:  strings.TrimSpace(in.AccessToken),
					RefreshToken: strings.TrimSpace(in.RefreshToken),
				},
				IDToken: strings.TrimSpace(in.IDToken),
			}
			if expiry := strings.TrimSpace(in.ExpiresAt); expiry != "" {
				if parsed, err := time.Parse(time.RFC3339, expiry); err == nil {
					sess.Tokens.Expiry = parsed
				}
			}
		}
		a.sessions.Put(r.Context(), sess)
		if sess.Tokens != nil && !bearerBootstrap {
			a.persistOAuthToken(r.Context(), "session_create", username, email, subject, a.oauthProviderName(), strings.TrimSpace(in.AccessToken), strings.TrimSpace(in.IDToken), strings.TrimSpace(in.RefreshToken), sess.Tokens.Expiry)
		}
		writeSessionCookie(w, a.cfg, a.sessions, sess.ID)
		runtimeJSON(w, http.StatusOK, map[string]any{"sessionId": sess.ID, "username": username})
	}
}
