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
		runtimeJSON(w, http.StatusOK, map[string]any{"authURL": resp.AuthURL, "state": resp.State, "provider": a.oauthProviderName(), "redirectURI": resp.RedirectURI, "delegated": true, "pkce": true, "responseType": "code"})
	}
}

func (a *authExtension) handleOAuthMobileInitiate() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		initiation, err := decodeOAuthInitiateRequest(r)
		if err != nil {
			runtimeError(w, http.StatusBadRequest, err)
			return
		}
		if strings.TrimSpace(initiation.RedirectURI) == "" {
			runtimeError(w, http.StatusBadRequest, fmt.Errorf("redirectURI is required for mobile oauth"))
			return
		}
		resp, err := a.buildOAuthInitiateResponseFor(r, initiation, oauthScopeTargetMobile)
		if err != nil {
			runtimeError(w, http.StatusBadRequest, err)
			return
		}
		runtimeJSON(w, http.StatusOK, map[string]any{
			"authURL":      resp.AuthURL,
			"state":        resp.State,
			"provider":     a.oauthProviderName(),
			"redirectURI":  resp.RedirectURI,
			"delegated":    true,
			"mobile":       true,
			"pkce":         true,
			"responseType": "code",
		})
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
		scopes := oauthScopesForTarget(a.cfg.OAuth.Client, oauthScopeTargetDefault, in.Scopes...)
		oauthCfg, err := loadOAuthClientConfig(r.Context(), configURL)
		if err != nil {
			runtimeError(w, http.StatusBadRequest, fmt.Errorf("unable to load oauth config: %w", err))
			return
		}
		cmd := &authorizer.Command{
			AuthFlow:   "OOB",
			UsePKCE:    true,
			SecretsURL: secretsURL,
			Scopes:     scopes,
			OAuthConfig: authorizer.OAuthConfig{
				Config: cloneOAuthConfigWithScopes(oauthCfg, scopes),
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
		if err := validateConfiguredOAuthScopes(a.cfg.OAuth.Client, scopes, idToken, token.AccessToken, token.RefreshToken); err != nil {
			runtimeError(w, http.StatusUnauthorized, err)
			return
		}
		if username == "" {
			username = "user"
		}
		provider := a.oauthProviderName()
		sess := &Session{
			ID:        uuid.New().String(),
			Username:  username,
			Email:     email,
			Subject:   subject,
			Provider:  provider,
			Scopes:    append([]string(nil), scopes...),
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
		a.canonicalizeSessionUser(r.Context(), sess)
		a.sessions.PutAsync(r.Context(), sess)
		writeSessionCookie(w, a.cfg, a.sessions, sess.ID)
		a.scheduleOAuthTokenPersist(r.Context(), "oauth_oob", username, email, subject, provider, token.AccessToken, idToken, token.RefreshToken, token.Expiry)
		runtimeJSON(w, http.StatusOK, map[string]any{"status": "ok", "sessionId": sess.ID, "username": username, "provider": provider})
	}
}

func (a *authExtension) buildOAuthInitiateResponse(r *http.Request) (*oauthInitiateResponse, error) {
	initiation, err := decodeOAuthInitiateRequest(r)
	if err != nil {
		return nil, err
	}
	return a.buildOAuthInitiateResponseFor(r, initiation, oauthScopeTargetWebUI)
}

func (a *authExtension) buildOAuthInitiateResponseFor(r *http.Request, initiation oauthInitiateRequest, target oauthScopeTarget) (*oauthInitiateResponse, error) {
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
	if requestedRedirectURI := strings.TrimSpace(initiation.RedirectURI); requestedRedirectURI != "" {
		if !a.isAllowedOAuthRedirectURI(requestedRedirectURI, redirectURI) {
			return nil, fmt.Errorf("oauth redirectURI is not allowed")
		}
		redirectURI = requestedRedirectURI
	}
	codeVerifier := flow.GenerateCodeVerifier()
	returnURL := strings.TrimSpace(firstNonEmpty(initiation.ReturnURL, r.URL.Query().Get("returnURL")))
	state, err := encryptOAuthState(r.Context(), configURL, oauthStatePayload{CodeVerifier: codeVerifier, ReturnURL: returnURL, RedirectURI: redirectURI})
	if err != nil {
		return nil, fmt.Errorf("unable to create oauth state: %w", err)
	}
	scopes := oauthScopesForTarget(a.cfg.OAuth.Client, target, initiation.Scopes...)
	oauthCfg = cloneOAuthConfigWithScopes(oauthCfg, scopes)
	state, err = encryptOAuthState(r.Context(), configURL, oauthStatePayload{
		CodeVerifier: codeVerifier,
		ReturnURL:    returnURL,
		RedirectURI:  redirectURI,
		Scopes:       scopes,
	})
	if err != nil {
		return nil, fmt.Errorf("unable to create oauth state: %w", err)
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
	return &oauthInitiateResponse{AuthURL: authURL, State: state, RedirectURI: redirectURI}, nil
}

type oauthInitiateRequest struct {
	RedirectURI string   `json:"redirectURI"`
	RedirectUri string   `json:"redirectUri"`
	ReturnURL   string   `json:"returnURL"`
	ReturnUrl   string   `json:"returnUrl"`
	Scopes      []string `json:"scopes,omitempty"`
}

func decodeOAuthInitiateRequest(r *http.Request) (oauthInitiateRequest, error) {
	var ret oauthInitiateRequest
	if r == nil {
		return ret, nil
	}
	ret.RedirectURI = strings.TrimSpace(firstNonEmpty(r.URL.Query().Get("redirectURI"), r.URL.Query().Get("redirectUri")))
	ret.ReturnURL = strings.TrimSpace(firstNonEmpty(r.URL.Query().Get("returnURL"), r.URL.Query().Get("returnUrl")))
	if r.Body == nil || r.ContentLength == 0 {
		return ret, nil
	}
	var body oauthInitiateRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return ret, fmt.Errorf("invalid oauth initiate request: %w", err)
	}
	if value := strings.TrimSpace(firstNonEmpty(body.RedirectURI, body.RedirectUri)); value != "" {
		ret.RedirectURI = value
	}
	if value := strings.TrimSpace(firstNonEmpty(body.ReturnURL, body.ReturnUrl)); value != "" {
		ret.ReturnURL = value
	}
	ret.Scopes = append([]string(nil), body.Scopes...)
	return ret, nil
}

func (a *authExtension) isAllowedOAuthRedirectURI(candidate, serverCallbackURI string) bool {
	candidate = strings.TrimSpace(candidate)
	if candidate == "" {
		return false
	}
	if candidate == strings.TrimSpace(serverCallbackURI) {
		return true
	}
	if a == nil || a.cfg == nil || a.cfg.OAuth == nil || a.cfg.OAuth.Client == nil {
		return false
	}
	if candidate == strings.TrimSpace(a.cfg.OAuth.Client.RedirectURI) {
		return true
	}
	for _, item := range a.cfg.OAuth.Client.RedirectURIs {
		if candidate == strings.TrimSpace(item) {
			return true
		}
	}
	return false
}

func (a *authExtension) handleOAuthCallback() http.HandlerFunc {
	return a.handleOAuthCallbackForSource("oauth_callback")
}

func (a *authExtension) handleOAuthMobileCallback() http.HandlerFunc {
	return a.handleOAuthCallbackForSource("oauth_mobile_callback")
}

func (a *authExtension) handleOAuthCallbackForSource(source string) http.HandlerFunc {
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
		redirectURI := strings.TrimSpace(statePayload.RedirectURI)
		if redirectURI == "" {
			redirectURI = callbackURL(r, a.cfg.RedirectPath)
		}
		token, err := flow.Exchange(r.Context(), oauthCfg, code, flow.WithRedirectURI(redirectURI), flow.WithPKCE(true), flow.WithCodeVerifier(codeVerifier))
		if err != nil {
			runtimeError(w, http.StatusUnauthorized, fmt.Errorf("oauth exchange failed: %w", err))
			return
		}
		username, subject, email, idToken := identityFromOAuthToken(token)
		if err := validateConfiguredOAuthScopes(a.cfg.OAuth.Client, statePayload.Scopes, idToken, token.AccessToken, token.RefreshToken); err != nil {
			runtimeError(w, http.StatusUnauthorized, err)
			return
		}
		if username == "" {
			username = "user"
		}
		provider := a.oauthProviderName()
		sess := &Session{
			ID:        uuid.New().String(),
			Username:  username,
			Email:     email,
			Subject:   subject,
			Provider:  provider,
			Scopes:    append([]string(nil), statePayload.Scopes...),
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
		if len(sess.Scopes) == 0 {
			sess.Scopes = tokenScopesFromStrings(idToken, token.AccessToken, token.RefreshToken)
		}
		a.canonicalizeSessionUser(r.Context(), sess)
		a.sessions.PutAsync(r.Context(), sess)
		writeSessionCookie(w, a.cfg, a.sessions, sess.ID)
		a.scheduleOAuthTokenPersist(r.Context(), source, username, email, subject, provider, token.AccessToken, idToken, token.RefreshToken, token.Expiry)
		if wantsJSON(r) || r.Method == http.MethodPost {
			runtimeJSON(w, http.StatusOK, map[string]any{"status": "ok", "sessionId": sess.ID, "username": username, "provider": provider})
			return
		}
		returnTo := strings.TrimSpace(statePayload.ReturnURL)
		if returnTo == "" {
			returnTo = "/"
		}
		http.Redirect(w, r, returnTo, http.StatusTemporaryRedirect)
	}
}
