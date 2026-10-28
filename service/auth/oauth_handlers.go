package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/viant/agently-core/internal/authlog"
	scyauth "github.com/viant/scy/auth"
	"github.com/viant/scy/auth/authorizer"
	"github.com/viant/scy/auth/flow"
	"golang.org/x/oauth2"
)

type oobOAuthAuthorizer interface {
	Authorize(context.Context, *authorizer.Command) (*oauth2.Token, error)
}

func (a *authExtension) handleIDPDelegate() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		logOAuthLogin(r, a.oauthProviderName(), "idp_delegate", "received", "", "", 0, nil)
		resp, err := a.buildOAuthInitiateResponse(r)
		if err != nil {
			logOAuthLogin(r, a.oauthProviderName(), "idp_delegate", "failed", "initiate", "", http.StatusBadRequest, nil)
			runtimeError(w, http.StatusBadRequest, err)
			return
		}
		logOAuthLogin(r, a.oauthProviderName(), "idp_delegate", "authorization_url_issued", "", resp.AuthURL, http.StatusOK, nil)
		runtimeJSON(w, http.StatusOK, map[string]any{"mode": "delegated", "idpLogin": resp.AuthURL, "provider": a.oauthProviderName(), "authURL": resp.AuthURL, "state": resp.State, "expiresIn": 300})
	}
}

func (a *authExtension) handleIDPLogin() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		logOAuthLogin(r, a.oauthProviderName(), "idp_login", "received", "", "", 0, nil)
		resp, err := a.buildOAuthInitiateResponse(r)
		if err != nil {
			logOAuthLogin(r, a.oauthProviderName(), "idp_login", "failed", "initiate", "", http.StatusBadRequest, nil)
			runtimeError(w, http.StatusBadRequest, err)
			return
		}
		logOAuthLogin(r, a.oauthProviderName(), "idp_login", "authorization_url_issued", "", resp.AuthURL, http.StatusTemporaryRedirect, nil)
		http.Redirect(w, r, resp.AuthURL, http.StatusTemporaryRedirect)
	}
}

func (a *authExtension) handleOAuthInitiate() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		logOAuthLogin(r, a.oauthProviderName(), "oauth_initiate", "received", "", "", 0, nil)
		resp, err := a.buildOAuthInitiateResponse(r)
		if err != nil {
			logOAuthLogin(r, a.oauthProviderName(), "oauth_initiate", "failed", "initiate", "", http.StatusBadRequest, nil)
			runtimeError(w, http.StatusBadRequest, err)
			return
		}
		logOAuthLogin(r, a.oauthProviderName(), "oauth_initiate", "authorization_url_issued", "", resp.AuthURL, http.StatusOK, nil)
		runtimeJSON(w, http.StatusOK, map[string]any{"authURL": resp.AuthURL, "state": resp.State, "provider": a.oauthProviderName(), "redirectURI": resp.RedirectURI, "delegated": true, "pkce": true, "responseType": "code"})
	}
}

func (a *authExtension) handleOAuthMobileInitiate() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		logOAuthLogin(r, a.oauthProviderName(), "oauth_mobile_initiate", "received", "", "", 0, nil)
		initiation, err := decodeOAuthInitiateRequest(r)
		if err != nil {
			logOAuthLogin(r, a.oauthProviderName(), "oauth_mobile_initiate", "failed", "invalid_request", "", http.StatusBadRequest, nil)
			runtimeError(w, http.StatusBadRequest, err)
			return
		}
		if strings.TrimSpace(initiation.RedirectURI) == "" {
			logOAuthLogin(r, a.oauthProviderName(), "oauth_mobile_initiate", "failed", "missing_redirect_uri", "", http.StatusBadRequest, nil)
			runtimeError(w, http.StatusBadRequest, fmt.Errorf("redirectURI is required for mobile oauth"))
			return
		}
		resp, err := a.buildOAuthInitiateResponseFor(r, initiation, oauthScopeTargetMobile)
		if err != nil {
			logOAuthLogin(r, a.oauthProviderName(), "oauth_mobile_initiate", "failed", "initiate", "", http.StatusBadRequest, nil)
			runtimeError(w, http.StatusBadRequest, err)
			return
		}
		logOAuthLogin(r, a.oauthProviderName(), "oauth_mobile_initiate", "authorization_url_issued", "", resp.AuthURL, http.StatusOK, nil)
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
		scopes := normalizeScopes(in.Scopes)
		if len(scopes) == 0 {
			scopes = OAuthScopesForHeadless(a.cfg.OAuth.Client)
		}
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
		oobAuthorizer := a.oauthOOB
		if oobAuthorizer == nil {
			oobAuthorizer = authorizer.New()
		}
		token, err := oobAuthorizer.Authorize(r.Context(), cmd)
		if err != nil {
			runtimeError(w, http.StatusUnauthorized, err)
			return
		}
		if token == nil {
			runtimeError(w, http.StatusUnauthorized, fmt.Errorf("oauth oob returned empty token"))
			return
		}
		username, subject, email, idToken := identityFromOAuthToken(token)
		if err := validateRefreshedOAuthScopes(a.cfg.OAuth.Client, scopes, token, idToken); err != nil {
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
		if a.tokenStore != nil {
			if err := a.persistOAuthToken(r.Context(), "oauth_oob", username, email, subject, provider, token.AccessToken, idToken, token.RefreshToken, token.Expiry); err != nil {
				runtimeError(w, http.StatusInternalServerError, err)
				return
			}
		}
		a.canonicalizeSessionUser(r.Context(), sess)
		a.sessions.PutAsync(r.Context(), sess)
		writeSessionCookie(w, r, a.cfg, a.sessions, sess.ID)
		if a.tokenStore == nil {
			a.scheduleOAuthTokenPersist(r.Context(), "oauth_oob", username, email, subject, provider, token.AccessToken, idToken, token.RefreshToken, token.Expiry)
		}
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
		provider := a.oauthProviderName()
		logOAuthLogin(r, provider, source, "received", "", "", 0, nil)
		fail := func(status int, reason string, err error) {
			logOAuthLogin(r, provider, source, "failed", reason, "", status, nil)
			runtimeError(w, status, err)
		}
		if a.cfg == nil || a.cfg.OAuth == nil || a.cfg.OAuth.Client == nil {
			fail(http.StatusBadRequest, "client_not_configured", fmt.Errorf("oauth client not configured"))
			return
		}
		configURL := strings.TrimSpace(a.cfg.OAuth.Client.ConfigURL)
		if configURL == "" {
			fail(http.StatusBadRequest, "config_url_missing", fmt.Errorf("oauth client configURL is required"))
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
			fail(http.StatusBadRequest, "code_or_state_missing", fmt.Errorf("missing oauth code/state"))
			return
		}
		oauthCfg, err := loadOAuthClientConfig(r.Context(), configURL)
		if err != nil {
			fail(http.StatusBadRequest, "config_load", fmt.Errorf("unable to load oauth config: %w", err))
			return
		}
		statePayload, err := decryptOAuthState(r.Context(), configURL, state)
		if err != nil {
			fail(http.StatusBadRequest, "state_invalid", fmt.Errorf("invalid oauth state: %w", err))
			return
		}
		codeVerifier := strings.TrimSpace(statePayload.CodeVerifier)
		if codeVerifier == "" {
			fail(http.StatusBadRequest, "code_verifier_missing", fmt.Errorf("invalid oauth state: missing code verifier"))
			return
		}
		redirectURI := strings.TrimSpace(statePayload.RedirectURI)
		if redirectURI == "" {
			redirectURI = callbackURL(r, a.cfg.RedirectPath)
		}
		token, err := flow.Exchange(r.Context(), oauthCfg, code, flow.WithRedirectURI(redirectURI), flow.WithPKCE(true), flow.WithCodeVerifier(codeVerifier))
		if err != nil {
			logOAuthLogin(r, provider, source, "failed", "token_exchange", oauthCfg.Endpoint.TokenURL, 0, err)
			runtimeError(w, http.StatusUnauthorized, fmt.Errorf("oauth exchange failed: %w", err))
			return
		}
		username, subject, email, idToken := identityFromOAuthToken(token)
		if err := validateRefreshedOAuthScopes(a.cfg.OAuth.Client, statePayload.Scopes, token, idToken); err != nil {
			fail(http.StatusUnauthorized, "scope_validation", err)
			return
		}
		if username == "" {
			username = "user"
		}
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
		if a.tokenStore != nil {
			if err := a.persistOAuthToken(r.Context(), source, username, email, subject, provider, token.AccessToken, idToken, token.RefreshToken, token.Expiry); err != nil {
				fail(http.StatusInternalServerError, "token_persist", err)
				return
			}
		}
		a.canonicalizeSessionUser(r.Context(), sess)
		a.sessions.PutAsync(r.Context(), sess)
		writeSessionCookie(w, r, a.cfg, a.sessions, sess.ID)
		logOAuthLogin(r, provider, source, "session_established", "", "", http.StatusOK, nil)
		if a.tokenStore == nil {
			a.scheduleOAuthTokenPersist(r.Context(), source, username, email, subject, provider, token.AccessToken, idToken, token.RefreshToken, token.Expiry)
		}
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

// logOAuthLogin records browser login boundaries without code, state, tokens,
// cookies, redirect queries, or upstream response bodies. An issued URL only
// means that Agently returned it; the browser may still fail to reach the IdP.
func logOAuthLogin(r *http.Request, provider, source, action, reason, endpoint string, status int, err error) {
	event := authlog.Event{
		Op:             "oauth_login",
		Provider:       provider,
		Source:         source,
		Reason:         reason,
		Endpoint:       endpoint,
		HTTPStatus:     status,
		Classification: "login",
		Action:         action,
	}
	if err != nil {
		event.Classification = "exchange_error"
		event.HTTPStatus, event.OAuthErrorCode = oauthRefreshErrorDetails(err)
		event.OAuthErrorCode = safeOAuthLoginErrorCode(event.OAuthErrorCode)
		var transport *url.Error
		if errors.As(err, &transport) {
			event.Classification = "transport"
			event.Reason = oauthLoginTransportReason(err)
		}
	}
	authlog.Log(r.Context(), event)
}

func safeOAuthLoginErrorCode(code string) string {
	switch code {
	case "", "invalid_request", "invalid_client", "invalid_grant", "unauthorized_client", "unsupported_grant_type", "invalid_scope", "access_denied", "server_error", "temporarily_unavailable":
		return code
	default:
		return "other"
	}
}

func oauthLoginTransportReason(err error) string {
	var dns *net.DNSError
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "token_exchange_timeout"
	case errors.As(err, &dns):
		return "token_exchange_dns"
	case errors.Is(err, syscall.ECONNREFUSED):
		return "token_exchange_connection_refused"
	default:
		return "token_exchange_transport"
	}
}
