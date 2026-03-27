package auth

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	scyauth "github.com/viant/scy/auth"
	"github.com/viant/scy/auth/authorizer"
	"github.com/viant/scy/auth/flow"
	"golang.org/x/oauth2"
)

func (a *authExtension) handleIDPDelegate() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		resp, err := a.buildOAuthInitiateResponse(r)
		if err != nil {
			runtimeError(w, http.StatusBadRequest, err)
			return
		}
		runtimeJSON(w, http.StatusOK, map[string]any{"mode": "delegated", "idpLogin": resp.AuthURL, "provider": a.oauthProviderName(), "authURL": resp.AuthURL, "state": resp.State, "expiresIn": 300})
	}
}

func (a *authExtension) handleIDPLogin() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		resp, err := a.buildOAuthInitiateResponse(r)
		if err != nil {
			runtimeError(w, http.StatusBadRequest, err)
			return
		}
		http.Redirect(w, r, resp.AuthURL, http.StatusTemporaryRedirect)
	}
}

func (a *authExtension) handleOAuthInitiate() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		resp, err := a.buildOAuthInitiateResponse(r)
		if err != nil {
			runtimeError(w, http.StatusBadRequest, err)
			return
		}
		runtimeJSON(w, http.StatusOK, map[string]any{"authURL": resp.AuthURL, "state": resp.State, "provider": a.oauthProviderName(), "delegated": true})
	}
}

func (a *authExtension) handleOAuthOOB() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if a.cfg == nil || a.cfg.OAuth == nil || a.cfg.OAuth.Client == nil {
			runtimeError(w, http.StatusBadRequest, fmt.Errorf("oauth client not configured"))
			return
		}
		var in struct {
			SecretsURL string   `json:"secretsURL"`
			Scopes     []string `json:"scopes,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			runtimeError(w, http.StatusBadRequest, err)
			return
		}
		secretsURL := strings.TrimSpace(in.SecretsURL)
		if secretsURL == "" {
			runtimeError(w, http.StatusBadRequest, fmt.Errorf("secretsURL is required"))
			return
		}
		configURL := strings.TrimSpace(a.cfg.OAuth.Client.ConfigURL)
		if configURL == "" {
			runtimeError(w, http.StatusBadRequest, fmt.Errorf("oauth client configURL is required"))
			return
		}
		scopes := in.Scopes
		if len(scopes) == 0 {
			scopes = append([]string(nil), a.cfg.OAuth.Client.Scopes...)
		}
		if len(scopes) == 0 {
			scopes = []string{"openid"}
		}
		cmd := &authorizer.Command{
			AuthFlow:   "OOB",
			UsePKCE:    true,
			SecretsURL: secretsURL,
			Scopes:     scopes,
			OAuthConfig: authorizer.OAuthConfig{
				ConfigURL: configURL,
			},
		}
		token, err := authorizer.New().Authorize(r.Context(), cmd)
		if err != nil {
			runtimeError(w, http.StatusUnauthorized, err)
			return
		}
		if token == nil {
			runtimeError(w, http.StatusUnauthorized, fmt.Errorf("oauth oob returned empty token"))
			return
		}
		username, subject, email, idToken := identityFromOAuthToken(token)
		if username == "" {
			username = "user"
		}
		provider := a.oauthProviderName()
		sess := &Session{
			ID:        uuid.New().String(),
			Username:  username,
			Email:     email,
			Subject:   subject,
			CreatedAt: time.Now(),
			Tokens: &scyauth.Token{
				Token: oauth2.Token{
					AccessToken:  token.AccessToken,
					RefreshToken: token.RefreshToken,
					Expiry:       token.Expiry,
				},
				IDToken: idToken,
			},
		}
		a.sessions.Put(r.Context(), sess)
		writeSessionCookie(w, a.cfg, a.sessions, sess.ID)
		a.persistOAuthToken(r.Context(), "oauth_oob", username, email, subject, provider, token.AccessToken, idToken, token.RefreshToken, token.Expiry)
		runtimeJSON(w, http.StatusOK, map[string]any{"status": "ok", "username": username, "provider": provider})
	}
}

func (a *authExtension) buildOAuthInitiateResponse(r *http.Request) (*oauthInitiateResponse, error) {
	if a.cfg == nil || a.cfg.OAuth == nil || a.cfg.OAuth.Client == nil {
		return nil, fmt.Errorf("oauth client not configured")
	}
	configURL := strings.TrimSpace(a.cfg.OAuth.Client.ConfigURL)
	if configURL == "" {
		return nil, fmt.Errorf("oauth client configURL is required for delegated login")
	}
	oauthCfg, err := loadOAuthClientConfig(r.Context(), configURL)
	if err != nil {
		return nil, fmt.Errorf("unable to load oauth config: %w", err)
	}
	redirectURI := callbackURL(r, a.cfg.RedirectPath)
	codeVerifier := flow.GenerateCodeVerifier()
	returnURL := strings.TrimSpace(r.URL.Query().Get("returnURL"))
	state, err := encryptOAuthState(r.Context(), configURL, oauthStatePayload{CodeVerifier: codeVerifier, ReturnURL: returnURL})
	if err != nil {
		return nil, fmt.Errorf("unable to create oauth state: %w", err)
	}
	scopes := a.cfg.OAuth.Client.Scopes
	if len(scopes) == 0 {
		scopes = []string{"openid", "profile", "email"}
	}
	authURL, err := flow.BuildAuthCodeURL(
		oauthCfg,
		flow.WithPKCE(true),
		flow.WithState(state),
		flow.WithRedirectURI(redirectURI),
		flow.WithScopes(scopes...),
		flow.WithCodeVerifier(codeVerifier),
	)
	if err != nil {
		return nil, fmt.Errorf("unable to build oauth authorize url: %w", err)
	}
	return &oauthInitiateResponse{AuthURL: authURL, State: state}, nil
}

func (a *authExtension) handleOAuthCallback() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if a.cfg == nil || a.cfg.OAuth == nil || a.cfg.OAuth.Client == nil {
			runtimeError(w, http.StatusBadRequest, fmt.Errorf("oauth client not configured"))
			return
		}
		configURL := strings.TrimSpace(a.cfg.OAuth.Client.ConfigURL)
		if configURL == "" {
			runtimeError(w, http.StatusBadRequest, fmt.Errorf("oauth client configURL is required"))
			return
		}
		code := strings.TrimSpace(r.URL.Query().Get("code"))
		state := strings.TrimSpace(r.URL.Query().Get("state"))
		if code == "" || state == "" {
			var body struct {
				Code  string `json:"code"`
				State string `json:"state"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
				if code == "" {
					code = strings.TrimSpace(body.Code)
				}
				if state == "" {
					state = strings.TrimSpace(body.State)
				}
			}
		}
		if code == "" || state == "" {
			runtimeError(w, http.StatusBadRequest, fmt.Errorf("missing oauth code/state"))
			return
		}
		oauthCfg, err := loadOAuthClientConfig(r.Context(), configURL)
		if err != nil {
			runtimeError(w, http.StatusBadRequest, fmt.Errorf("unable to load oauth config: %w", err))
			return
		}
		statePayload, err := decryptOAuthState(r.Context(), configURL, state)
		if err != nil {
			runtimeError(w, http.StatusBadRequest, fmt.Errorf("invalid oauth state: %w", err))
			return
		}
		codeVerifier := strings.TrimSpace(statePayload.CodeVerifier)
		if codeVerifier == "" {
			runtimeError(w, http.StatusBadRequest, fmt.Errorf("invalid oauth state: missing code verifier"))
			return
		}
		redirectURI := callbackURL(r, a.cfg.RedirectPath)
		token, err := flow.Exchange(r.Context(), oauthCfg, code, flow.WithRedirectURI(redirectURI), flow.WithPKCE(true), flow.WithCodeVerifier(codeVerifier))
		if err != nil {
			runtimeError(w, http.StatusUnauthorized, fmt.Errorf("oauth exchange failed: %w", err))
			return
		}
		username, subject, email, idToken := identityFromOAuthToken(token)
		if username == "" {
			username = "user"
		}
		provider := a.oauthProviderName()
		sess := &Session{
			ID:        uuid.New().String(),
			Username:  username,
			Email:     email,
			Subject:   subject,
			CreatedAt: time.Now(),
			Tokens: &scyauth.Token{
				Token: oauth2.Token{
					AccessToken:  token.AccessToken,
					RefreshToken: token.RefreshToken,
					Expiry:       token.Expiry,
				},
				IDToken: idToken,
			},
		}
		a.sessions.Put(r.Context(), sess)
		writeSessionCookie(w, a.cfg, a.sessions, sess.ID)
		a.persistOAuthToken(r.Context(), "oauth_callback", username, email, subject, provider, token.AccessToken, idToken, token.RefreshToken, token.Expiry)
		if wantsJSON(r) {
			runtimeJSON(w, http.StatusOK, map[string]any{"status": "ok", "username": username, "provider": provider})
			return
		}
		returnTo := strings.TrimSpace(statePayload.ReturnURL)
		if returnTo == "" {
			returnTo = "/"
		}
		http.Redirect(w, r, returnTo, http.StatusTemporaryRedirect)
	}
}
