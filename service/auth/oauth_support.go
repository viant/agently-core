package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
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
	AuthURL string
	State   string
}

type oauthStatePayload struct {
	CodeVerifier string `json:"codeVerifier"`
	ReturnURL    string `json:"returnURL,omitempty"`
}

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
