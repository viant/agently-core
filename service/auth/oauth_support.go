package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/viant/scy/auth/authorizer"
	"github.com/viant/scy/kms"
	"github.com/viant/scy/kms/blowfish"
	"golang.org/x/oauth2"
)

type oauthInitiateResponse struct {
	AuthURL     string
	State       string
	RedirectURI string
}

type oauthStatePayload struct {
	CodeVerifier string   `json:"codeVerifier"`
	ReturnURL    string   `json:"returnURL,omitempty"`
	RedirectURI  string   `json:"redirectURI,omitempty"`
	Scopes       []string `json:"scopes,omitempty"`
}

type oauthScopeTarget string

const (
	oauthScopeTargetDefault oauthScopeTarget = ""
	oauthScopeTargetWebUI   oauthScopeTarget = "web"
	oauthScopeTargetMobile  oauthScopeTarget = "mobile"
	oauthScopeTargetCLI     oauthScopeTarget = "cli"
)

func callbackURL(r *http.Request, configuredPath string) string {
	scheme := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto"))
	if scheme == "" {
		if r.TLS != nil {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}
	host := strings.TrimSpace(r.Header.Get("X-Forwarded-Host"))
	if host == "" {
		host = r.Host
	}
	path := strings.TrimSpace(configuredPath)
	if path == "" {
		path = "/v1/api/auth/oauth/callback"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return scheme + "://" + host + path
}

func identityFromOAuthToken(tok *oauth2.Token) (username, subject, email, idToken string) {
	if tok == nil {
		return "", "", "", ""
	}
	if raw := tok.Extra("id_token"); raw != nil {
		if s, ok := raw.(string); ok {
			idToken = strings.TrimSpace(s)
		}
	}
	return identityFromTokenStrings(idToken, tok.AccessToken)
}

func identityFromTokenStrings(idToken, accessToken string) (username, subject, email, rawID string) {
	rawID = strings.TrimSpace(idToken)
	claims := parseJWTClaims(rawID)
	if len(claims) == 0 {
		claims = parseJWTClaims(strings.TrimSpace(accessToken))
	}
	subject = claimString(claims, "sub")
	email = claimString(claims, "email")
	username = strings.TrimSpace(claimString(claims, "preferred_username"))
	if username == "" {
		username = strings.TrimSpace(claimString(claims, "name"))
	}
	if username == "" && email != "" {
		if idx := strings.Index(email, "@"); idx > 0 {
			username = email[:idx]
		} else {
			username = email
		}
	}
	if username == "" {
		username = subject
	}
	return username, subject, email, rawID
}

func parseJWTClaims(token string) map[string]interface{} {
	token = strings.TrimSpace(token)
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return map[string]interface{}{}
	}
	seg := parts[1]
	switch len(seg) % 4 {
	case 2:
		seg += "=="
	case 3:
		seg += "="
	}
	data, err := base64.URLEncoding.DecodeString(seg)
	if err != nil {
		return map[string]interface{}{}
	}
	out := map[string]interface{}{}
	if err := json.Unmarshal(data, &out); err != nil {
		return map[string]interface{}{}
	}
	return out
}

func bearerTokenFromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(header) < 8 || !strings.EqualFold(header[:7], "Bearer ") {
		return ""
	}
	return strings.TrimSpace(header[7:])
}

func claimString(claims map[string]interface{}, key string) string {
	raw, ok := claims[key]
	if !ok {
		return ""
	}
	val, ok := raw.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(val)
}

func claimUnixTime(claims map[string]interface{}, key string) (time.Time, bool) {
	raw, ok := claims[key]
	if !ok || raw == nil {
		return time.Time{}, false
	}
	switch actual := raw.(type) {
	case float64:
		return time.Unix(int64(actual), 0).UTC(), true
	case json.Number:
		if n, err := actual.Int64(); err == nil {
			return time.Unix(n, 0).UTC(), true
		}
	case int64:
		return time.Unix(actual, 0).UTC(), true
	case int:
		return time.Unix(int64(actual), 0).UTC(), true
	}
	return time.Time{}, false
}

func tokenExpiryFromTokenStrings(idToken, accessToken string) time.Time {
	for _, candidate := range []string{strings.TrimSpace(idToken), strings.TrimSpace(accessToken)} {
		if candidate == "" {
			continue
		}
		if exp, ok := claimUnixTime(parseJWTClaims(candidate), "exp"); ok {
			return exp
		}
	}
	return time.Time{}
}

func resolveTokenExpiry(explicitExpiresAt, idToken, accessToken string) time.Time {
	if expiry := strings.TrimSpace(explicitExpiresAt); expiry != "" {
		if parsed, err := time.Parse(time.RFC3339, expiry); err == nil {
			return parsed
		}
	}
	return tokenExpiryFromTokenStrings(idToken, accessToken)
}

func oauthScopesForTarget(client *OAuthClient, target oauthScopeTarget, explicit ...string) []string {
	if len(explicit) > 0 {
		if scopes := normalizeScopes(explicit); len(scopes) > 0 {
			return scopes
		}
	}
	if client != nil {
		baseScopes := normalizeScopes(client.Scopes)
		var surfaceScopes []string
		switch target {
		case oauthScopeTargetWebUI:
			surfaceScopes = normalizeScopes(client.WebUIScopes)
		case oauthScopeTargetMobile:
			surfaceScopes = normalizeScopes(client.MobileUIScopes)
		case oauthScopeTargetCLI:
			surfaceScopes = normalizeScopes(client.CLIScopes)
		}
		if len(surfaceScopes) > 0 {
			return normalizeScopes(append(append([]string(nil), baseScopes...), surfaceScopes...))
		}
		if len(baseScopes) > 0 {
			return baseScopes
		}
	}
	if target == oauthScopeTargetDefault {
		return []string{"openid"}
	}
	return []string{"openid", "profile", "email"}
}

// OAuthScopesForHeadless returns the grant a non-interactive user-owned
// runtime should request. A dedicated CLI scope wins; otherwise legacy
// browser-created schedules inherit the web scope, with mobile as the final
// compatibility fallback.
func OAuthScopesForHeadless(client *OAuthClient) []string {
	if client == nil {
		return []string{"openid"}
	}
	baseScopes := normalizeScopes(client.Scopes)
	surfaceScopes := normalizeScopes(client.CLIScopes)
	if len(surfaceScopes) == 0 {
		surfaceScopes = normalizeScopes(client.WebUIScopes)
	}
	if len(surfaceScopes) == 0 {
		surfaceScopes = normalizeScopes(client.MobileUIScopes)
	}
	scopes := normalizeScopes(append(append([]string(nil), baseScopes...), surfaceScopes...))
	if len(scopes) == 0 {
		return []string{"openid"}
	}
	return scopes
}

func normalizeScopes(scopes []string) []string {
	if len(scopes) == 0 {
		return nil
	}
	result := make([]string, 0, len(scopes))
	seen := map[string]bool{}
	for _, scope := range scopes {
		scope = strings.TrimSpace(scope)
		if scope == "" || seen[scope] {
			continue
		}
		seen[scope] = true
		result = append(result, scope)
	}
	return result
}

func configuredOAuthScopeSets(client *OAuthClient) [][]string {
	if client == nil {
		return nil
	}
	candidates := [][]string{
		normalizeScopes(client.WebUIScopes),
		normalizeScopes(client.MobileUIScopes),
		normalizeScopes(client.CLIScopes),
		normalizeScopes(client.Scopes),
	}
	result := make([][]string, 0, len(candidates))
	seen := map[string]bool{}
	for _, candidate := range candidates {
		if len(candidate) == 0 {
			continue
		}
		key := strings.Join(candidate, "\x00")
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, candidate)
	}
	return result
}

var oidcScopeSet = map[string]bool{
	"openid":         true,
	"profile":        true,
	"email":          true,
	"phone":          true,
	"address":        true,
	"offline_access": true,
}

func tokenScopesFromStrings(tokens ...string) []string {
	merged := make([]string, 0, len(tokens))
	for _, token := range tokens {
		if scopes := tokenScopes(token); len(scopes) > 0 {
			merged = append(merged, scopes...)
		}
	}
	return normalizeScopes(merged)
}

func tokenScopes(token string) []string {
	claims := parseJWTClaims(token)
	if len(claims) == 0 {
		return nil
	}
	if scope := claimString(claims, "scope"); scope != "" {
		return normalizeScopes(strings.Fields(scope))
	}
	if raw, ok := claims["scp"]; ok {
		switch actual := raw.(type) {
		case string:
			return normalizeScopes(strings.Fields(actual))
		case []interface{}:
			values := make([]string, 0, len(actual))
			for _, item := range actual {
				if s, ok := item.(string); ok {
					values = append(values, s)
				}
			}
			return normalizeScopes(values)
		}
	}
	return nil
}

func tokenAudience(token string) string {
	claims := parseJWTClaims(token)
	if len(claims) == 0 {
		return ""
	}
	switch actual := claims["aud"].(type) {
	case string:
		return strings.TrimSpace(actual)
	case []interface{}:
		for _, item := range actual {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				return strings.TrimSpace(s)
			}
		}
	}
	return ""
}

func hasAllScopes(actual, required []string) bool {
	if len(required) == 0 {
		return true
	}
	if len(actual) == 0 {
		return false
	}
	set := map[string]bool{}
	for _, item := range normalizeScopes(actual) {
		set[item] = true
	}
	for _, item := range normalizeScopes(required) {
		if !set[item] {
			return false
		}
	}
	return true
}

func discriminatorScopes(scopes []string) []string {
	scopes = normalizeScopes(scopes)
	if len(scopes) == 0 {
		return nil
	}
	result := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		if oidcScopeSet[scope] {
			continue
		}
		result = append(result, scope)
	}
	return result
}

func hasAnyScope(actual, allowed []string) bool {
	if len(allowed) == 0 {
		return false
	}
	set := map[string]bool{}
	for _, item := range normalizeScopes(actual) {
		set[item] = true
	}
	for _, item := range normalizeScopes(allowed) {
		if set[item] {
			return true
		}
	}
	return false
}

func validateConfiguredOAuthScopes(client *OAuthClient, expected []string, tokens ...string) error {
	expected = normalizeScopes(expected)
	tokenScopes := tokenScopesFromStrings(tokens...)
	if len(expected) > 0 {
		if discriminator := discriminatorScopes(expected); len(discriminator) > 0 {
			if hasAnyScope(tokenScopes, discriminator) {
				return nil
			}
			return fmt.Errorf("oauth token is missing required scope marker: need one of [%s]", strings.Join(discriminator, "] ["))
		}
		if hasAllScopes(tokenScopes, expected) {
			return nil
		}
		return fmt.Errorf("oauth token is missing required scopes: need %s", strings.Join(expected, " "))
	}
	configured := configuredOAuthScopeSets(client)
	if len(configured) == 0 {
		return nil
	}
	for _, candidate := range configured {
		if discriminator := discriminatorScopes(candidate); len(discriminator) > 0 {
			if hasAnyScope(tokenScopes, discriminator) {
				return nil
			}
			continue
		}
		if hasAllScopes(tokenScopes, candidate) {
			return nil
		}
	}
	options := make([]string, 0, len(configured))
	for _, item := range configured {
		options = append(options, strings.Join(item, " "))
	}
	return fmt.Errorf("oauth token is missing configured scopes: need one of [%s]", strings.Join(options, "] ["))
}

func cloneOAuthConfigWithScopes(config *oauth2.Config, scopes []string) *oauth2.Config {
	if config == nil {
		return nil
	}
	clone := *config
	clone.Scopes = append([]string(nil), normalizeScopes(scopes)...)
	return &clone
}

func oauthRefreshScopes(sess *Session, token *OAuthToken) []string {
	if sess != nil {
		if scopes := normalizeScopes(sess.Scopes); len(scopes) > 0 {
			return scopes
		}
		if sess.Tokens != nil {
			if scopes := tokenScopesFromStrings(sess.Tokens.RefreshToken, sess.Tokens.IDToken, sess.Tokens.AccessToken); len(scopes) > 0 {
				return scopes
			}
		}
	}
	if token != nil {
		if scopes := tokenScopesFromStrings(token.RefreshToken, token.IDToken, token.AccessToken); len(scopes) > 0 {
			return scopes
		}
	}
	return nil
}

func oauthRefreshScopesForClient(sess *Session, token *OAuthToken, client *OAuthClient) []string {
	scopes := oauthRefreshScopes(sess, token)
	if len(discriminatorScopes(scopes)) > 0 {
		return scopes
	}
	return normalizeScopes(append(scopes, OAuthScopesForHeadless(client)...))
}

func oauthRefreshResource(sess *Session, token *OAuthToken, clientID string) string {
	clientID = strings.TrimSpace(clientID)
	candidates := []string{}
	if sess != nil && sess.Tokens != nil {
		candidates = append(candidates,
			tokenAudience(sess.Tokens.RefreshToken),
			tokenAudience(sess.Tokens.AccessToken),
			tokenAudience(sess.Tokens.IDToken),
		)
	}
	if token != nil {
		candidates = append(candidates,
			tokenAudience(token.RefreshToken),
			tokenAudience(token.AccessToken),
			tokenAudience(token.IDToken),
		)
	}
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if clientID != "" && candidate == clientID {
			return ""
		}
		return candidate
	}
	return ""
}

func refreshOAuthToken(ctx context.Context, config *oauth2.Config, base *oauth2.Token, scopes []string, resource string) (*oauth2.Token, error) {
	if config == nil {
		return nil, fmt.Errorf("oauth2 config is nil")
	}
	if base == nil {
		return nil, fmt.Errorf("oauth refresh token is nil")
	}
	if strings.TrimSpace(base.RefreshToken) == "" {
		return nil, fmt.Errorf("oauth refresh token is empty")
	}
	scopes = normalizeScopes(scopes)
	resource = strings.TrimSpace(resource)
	if len(scopes) == 0 && resource == "" {
		return config.TokenSource(ctx, base).Token()
	}
	values := url.Values{}
	values.Set("grant_type", "refresh_token")
	values.Set("refresh_token", strings.TrimSpace(base.RefreshToken))
	if len(scopes) > 0 {
		values.Set("scope", strings.Join(scopes, " "))
	}
	if resource != "" {
		values.Set("resource", resource)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, config.Endpoint.TokenURL, strings.NewReader(values.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if strings.TrimSpace(config.ClientID) != "" {
		req.SetBasicAuth(strings.TrimSpace(config.ClientID), strings.TrimSpace(config.ClientSecret))
	}
	client := http.DefaultClient
	if httpClient, ok := ctx.Value(oauth2.HTTPClient).(*http.Client); ok && httpClient != nil {
		client = httpClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &oauth2.RetrieveError{
			Response: resp,
			Body:     body,
		}
	}
	var payload struct {
		AccessToken  string      `json:"access_token"`
		TokenType    string      `json:"token_type"`
		RefreshToken string      `json:"refresh_token"`
		ExpiresIn    int64       `json:"expires_in"`
		Scope        string      `json:"scope"`
		IDToken      string      `json:"id_token"`
		Extra        interface{} `json:"-"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	token := &oauth2.Token{
		AccessToken:  strings.TrimSpace(payload.AccessToken),
		TokenType:    strings.TrimSpace(payload.TokenType),
		RefreshToken: strings.TrimSpace(payload.RefreshToken),
	}
	if payload.ExpiresIn > 0 {
		token.Expiry = time.Now().UTC().Add(time.Duration(payload.ExpiresIn) * time.Second)
	}
	extras := map[string]any{}
	if strings.TrimSpace(payload.IDToken) != "" {
		extras["id_token"] = strings.TrimSpace(payload.IDToken)
	}
	if scope := strings.TrimSpace(payload.Scope); scope != "" {
		extras["scope"] = scope
	}
	if len(extras) > 0 {
		token = token.WithExtra(extras)
	}
	if token.RefreshToken == "" {
		token.RefreshToken = strings.TrimSpace(base.RefreshToken)
	}
	return token, nil
}

var stateCipher = blowfish.Cipher{}

func encryptState(ctx context.Context, salt, value string) (string, error) {
	key := &kms.Key{Kind: "raw", Raw: string(blowfish.EnsureKey([]byte(strings.TrimSpace(salt))))}
	encrypted, err := stateCipher.Encrypt(ctx, key, []byte(value))
	if err != nil {
		return "", err
	}
	return strings.TrimRight(base64.URLEncoding.EncodeToString(encrypted), "="), nil
}

func encryptOAuthState(ctx context.Context, salt string, payload oauthStatePayload) (string, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return encryptState(ctx, salt, string(data))
}

func decryptState(ctx context.Context, salt, state string) (string, error) {
	raw := strings.TrimSpace(state)
	switch len(raw) % 4 {
	case 2:
		raw += "=="
	case 3:
		raw += "="
	}
	data, err := base64.URLEncoding.DecodeString(raw)
	if err != nil {
		return "", err
	}
	key := &kms.Key{Kind: "raw", Raw: string(blowfish.EnsureKey([]byte(strings.TrimSpace(salt))))}
	decrypted, err := stateCipher.Decrypt(ctx, key, data)
	if err != nil {
		return "", err
	}
	return string(decrypted), nil
}

func decryptOAuthState(ctx context.Context, salt, state string) (oauthStatePayload, error) {
	raw, err := decryptState(ctx, salt, state)
	if err != nil {
		return oauthStatePayload{}, err
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return oauthStatePayload{}, fmt.Errorf("empty state payload")
	}
	if strings.HasPrefix(raw, "{") {
		var payload oauthStatePayload
		if err := json.Unmarshal([]byte(raw), &payload); err != nil {
			return oauthStatePayload{}, err
		}
		return payload, nil
	}
	return oauthStatePayload{CodeVerifier: raw}, nil
}

func loadOAuthClientConfig(ctx context.Context, configURL string) (*oauth2.Config, error) {
	oa := authorizer.New()
	oc := &authorizer.OAuthConfig{ConfigURL: configURL}
	if err := oa.EnsureConfig(ctx, oc); err == nil && oc.Config != nil {
		return oc.Config, nil
	}
	path := strings.TrimSpace(configURL)
	if strings.HasPrefix(path, "file://") {
		if u, err := url.Parse(path); err == nil {
			path = u.Path
		}
	}
	if strings.Contains(path, "://") && !strings.HasPrefix(path, "/") {
		return nil, fmt.Errorf("unsupported oauth config url: %s", configURL)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw struct {
		AuthURL      string   `json:"authURL"`
		TokenURL     string   `json:"tokenURL"`
		ClientID     string   `json:"clientID"`
		ClientSecret string   `json:"clientSecret"`
		RedirectURL  string   `json:"redirectURL"`
		Scopes       []string `json:"scopes"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	if strings.TrimSpace(raw.AuthURL) == "" || strings.TrimSpace(raw.TokenURL) == "" || strings.TrimSpace(raw.ClientID) == "" {
		return nil, fmt.Errorf("oauth config requires authURL, tokenURL, and clientID")
	}
	return &oauth2.Config{
		ClientID:     strings.TrimSpace(raw.ClientID),
		ClientSecret: strings.TrimSpace(raw.ClientSecret),
		RedirectURL:  strings.TrimSpace(raw.RedirectURL),
		Scopes:       append([]string(nil), raw.Scopes...),
		Endpoint: oauth2.Endpoint{
			AuthURL:  strings.TrimSpace(raw.AuthURL),
			TokenURL: strings.TrimSpace(raw.TokenURL),
		},
	}, nil
}
