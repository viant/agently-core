package auth

import (
	"context"
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
			if runtimeConfirmedMissing(r.Context()) {
				if cookieName := strings.TrimSpace(a.cfg.CookieName); cookieName != "" {
					http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", HttpOnly: true, MaxAge: -1})
				}
				runtimeError(w, http.StatusUnauthorized, fmt.Errorf("oauth session is missing a valid token"))
				return
			}
			if user := RuntimeUserFromContext(r.Context()); user != nil {
				displayName := strings.TrimSpace(user.Subject)
				if resolved := a.lookupDisplayUserBySubject(r.Context(), strings.TrimSpace(user.Subject), a.oauthProviderName()); resolved != nil {
					if v := strings.TrimSpace(resolved.DisplayName); v != "" {
						displayName = v
					} else if v := strings.TrimSpace(resolved.Username); v != "" {
						displayName = v
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
		if a.requiresOAuthTokensForSession(sess) {
			availability := a.sessionOAuthTokenAvailability(r.Context(), sess)
			switch availability.state {
			case tokenConfirmedMissing:
				if cookieName := strings.TrimSpace(a.cfg.CookieName); cookieName != "" {
					if c, err := r.Cookie(cookieName); err == nil && strings.TrimSpace(c.Value) != "" {
						a.sessions.Delete(r.Context(), strings.TrimSpace(c.Value))
					}
					http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", HttpOnly: true, MaxAge: -1})
				}
				runtimeError(w, http.StatusUnauthorized, fmt.Errorf("oauth session is missing a valid token"))
				return
			case tokenPreserveWithoutInjection:
				// Identity remains usable while token ownership or persistence is
				// temporarily unavailable. No credentials are exposed by /auth/me.
			}
		}
		displayName := strings.TrimSpace(sess.Username)
		if resolved := a.lookupDisplayUserBySubject(r.Context(), strings.TrimSpace(sess.Subject), strings.TrimSpace(firstNonEmpty(sess.Provider, a.oauthProviderName()))); resolved != nil {
			if v := strings.TrimSpace(resolved.DisplayName); v != "" {
				displayName = v
			} else if v := strings.TrimSpace(resolved.Username); v != "" {
				displayName = v
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

func (a *authExtension) lookupDisplayUserBySubject(ctx context.Context, subject, provider string) *User {
	if a == nil || a.users == nil || strings.TrimSpace(subject) == "" || strings.TrimSpace(provider) == "" {
		return nil
	}
	lookupCtx, cancel := durableAuthLookupContext(ctx)
	defer cancel()
	type result struct {
		user *User
		err  error
	}
	done := make(chan result, 1)
	go func() {
		user, err := a.users.GetBySubjectAndProvider(lookupCtx, strings.TrimSpace(subject), strings.TrimSpace(provider))
		done <- result{user: user, err: err}
	}()
	select {
	case out := <-done:
		if out.err != nil {
			return nil
		}
		return out.user
	case <-lookupCtx.Done():
		return nil
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
					"name":           a.oauthProviderName(),
					"label":          firstNonEmpty(a.cfg.OAuth.Label, "OIDC"),
					"type":           "oidc",
					"clientID":       strings.TrimSpace(a.cfg.OAuth.Client.ClientID),
					"discoveryURL":   strings.TrimSpace(a.cfg.OAuth.Client.DiscoveryURL),
					"redirectURI":    strings.TrimSpace(a.cfg.OAuth.Client.RedirectURI),
					"scopes":         append([]string(nil), a.cfg.OAuth.Client.Scopes...),
					"webUIScopes":    append([]string(nil), a.cfg.OAuth.Client.WebUIScopes...),
					"mobileUIScopes": append([]string(nil), a.cfg.OAuth.Client.MobileUIScopes...),
					"cliScopes":      append([]string(nil), a.cfg.OAuth.Client.CLIScopes...),
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
		scopes := append([]string{}, a.cfg.OAuth.Client.Scopes...)
		runtimeJSON(w, http.StatusOK, map[string]any{
			"mode":            strings.TrimSpace(a.cfg.OAuth.Mode),
			"configURL":       strings.TrimSpace(a.cfg.OAuth.Client.ConfigURL),
			"clientID":        strings.TrimSpace(a.cfg.OAuth.Client.ClientID),
			"discoveryURL":    strings.TrimSpace(a.cfg.OAuth.Client.DiscoveryURL),
			"redirectURI":     strings.TrimSpace(a.cfg.OAuth.Client.RedirectURI),
			"redirectURIs":    append([]string(nil), a.cfg.OAuth.Client.RedirectURIs...),
			"usePopupLogin":   a.cfg.OAuth.UsePopupLogin,
			"redirectSameTab": !a.cfg.OAuth.UsePopupLogin,
			"scopes":          scopes,
			"webUIScopes":     append([]string(nil), a.cfg.OAuth.Client.WebUIScopes...),
			"mobileUIScopes":  append([]string(nil), a.cfg.OAuth.Client.MobileUIScopes...),
			"cliScopes":       append([]string(nil), a.cfg.OAuth.Client.CLIScopes...),
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
		scopes := tokenScopesFromStrings(strings.TrimSpace(in.IDToken), strings.TrimSpace(in.AccessToken), strings.TrimSpace(in.RefreshToken))
		var oauthClient *OAuthClient
		if a != nil && a.cfg != nil && a.cfg.OAuth != nil {
			oauthClient = a.cfg.OAuth.Client
		}
		if err := validateConfiguredOAuthScopes(oauthClient, nil, strings.TrimSpace(in.IDToken), strings.TrimSpace(in.AccessToken), strings.TrimSpace(in.RefreshToken)); err != nil {
			runtimeError(w, http.StatusUnauthorized, err)
			return
		}
		sess := &Session{
			ID:        uuid.New().String(),
			Username:  username,
			Email:     email,
			Subject:   subject,
			Provider:  a.oauthProviderName(),
			Scopes:    scopes,
			CreatedAt: time.Now(),
		}
		if strings.TrimSpace(in.AccessToken) != "" || strings.TrimSpace(in.IDToken) != "" || strings.TrimSpace(in.RefreshToken) != "" {
			expiry := resolveTokenExpiry(strings.TrimSpace(in.ExpiresAt), strings.TrimSpace(in.IDToken), strings.TrimSpace(in.AccessToken))
			sess.Tokens = &scyauth.Token{
				Token: oauth2.Token{
					AccessToken:  strings.TrimSpace(in.AccessToken),
					RefreshToken: strings.TrimSpace(in.RefreshToken),
					Expiry:       expiry,
				},
				IDToken: strings.TrimSpace(in.IDToken),
			}
		}
		if sess.Tokens != nil && !bearerBootstrap && a.tokenStore != nil {
			if err := a.persistOAuthToken(r.Context(), "session_create", username, email, subject, a.oauthProviderName(), strings.TrimSpace(in.AccessToken), strings.TrimSpace(in.IDToken), strings.TrimSpace(in.RefreshToken), sess.Tokens.Expiry); err != nil {
				runtimeError(w, http.StatusServiceUnavailable, err)
				return
			}
		}
		a.sessions.PutAsync(r.Context(), sess)
		writeSessionCookie(w, a.cfg, a.sessions, sess.ID)
		runtimeJSON(w, http.StatusOK, map[string]any{"sessionId": sess.ID, "username": username})
	}
}

func (a *authExtension) handleAttachSession() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			SessionID string `json:"sessionId"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			runtimeError(w, http.StatusBadRequest, err)
			return
		}
		sessionID := strings.TrimSpace(in.SessionID)
		if sessionID == "" {
			runtimeError(w, http.StatusBadRequest, fmt.Errorf("sessionId is required"))
			return
		}
		if a == nil || a.sessions == nil {
			runtimeError(w, http.StatusServiceUnavailable, fmt.Errorf("session store unavailable"))
			return
		}
		sess := a.sessions.Get(r.Context(), sessionID)
		if sess == nil {
			runtimeError(w, http.StatusNotFound, fmt.Errorf("session not found"))
			return
		}
		if a.requiresOAuthTokensForSession(sess) {
			availability := a.sessionOAuthTokenAvailability(r.Context(), sess)
			switch availability.state {
			case tokenConfirmedMissing:
				a.sessions.Delete(r.Context(), sessionID)
				runtimeError(w, http.StatusUnauthorized, fmt.Errorf("oauth session is missing a valid token"))
				return
			case tokenPreserveWithoutInjection:
				runtimeError(w, http.StatusServiceUnavailable, fmt.Errorf("oauth token state is temporarily unavailable"))
				return
			}
		}
		writeSessionCookie(w, a.cfg, a.sessions, sess.ID)
		runtimeJSON(w, http.StatusOK, map[string]any{
			"status":    "ok",
			"sessionId": sess.ID,
			"username":  strings.TrimSpace(sess.Username),
		})
	}
}
