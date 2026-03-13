package sdk

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/viant/scy/auth/authorizer"
	"golang.org/x/oauth2"
)

type AuthProvider struct {
	Name  string `json:"name"`
	Label string `json:"label"`
	Mode  string `json:"mode"`
}

type LocalLoginRequest struct {
	Name string `json:"name"`
}

type OAuthConfigResponse struct {
	ConfigURL string   `json:"configURL"`
	Scopes    []string `json:"scopes,omitempty"`
}

type OOBRequest struct {
	SecretsURL string   `json:"secretsURL"`
	Scopes     []string `json:"scopes,omitempty"`
}

func (c *HTTPClient) AuthProviders(ctx context.Context) ([]AuthProvider, error) {
	var out []AuthProvider
	if err := c.doJSON(ctx, "GET", "/v1/api/auth/providers", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *HTTPClient) AuthMe(ctx context.Context) (map[string]interface{}, error) {
	var out map[string]interface{}
	if err := c.doJSON(ctx, "GET", "/v1/api/auth/me", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *HTTPClient) AuthLocalLogin(ctx context.Context, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("name is required")
	}
	return c.doJSON(ctx, "POST", "/v1/api/auth/local/login", &LocalLoginRequest{Name: name}, nil)
}

func (c *HTTPClient) AuthOAuthConfig(ctx context.Context) (*OAuthConfigResponse, error) {
	var out OAuthConfigResponse
	if err := c.doJSON(ctx, "GET", "/v1/api/auth/oauth/config", nil, &out); err == nil && strings.TrimSpace(out.ConfigURL) != "" {
		return &out, nil
	}
	var wrapped struct {
		Status string               `json:"status"`
		Data   *OAuthConfigResponse `json:"data"`
	}
	if err := c.doJSON(ctx, "GET", "/v1/api/auth/oauth/config", nil, &wrapped); err != nil {
		return nil, err
	}
	if wrapped.Data == nil || strings.TrimSpace(wrapped.Data.ConfigURL) == "" {
		return nil, fmt.Errorf("oauth config not available")
	}
	return wrapped.Data, nil
}

func (c *HTTPClient) AuthSessionExchange(ctx context.Context, idToken string) error {
	idToken = strings.TrimSpace(idToken)
	if idToken == "" {
		return fmt.Errorf("id token is required")
	}
	req, err := c.newRequest(ctx, "POST", "/v1/api/auth/session", strings.NewReader("{}"), "application/json")
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+idToken)
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("session exchange failed: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return nil
}

func (c *HTTPClient) AuthBrowserSession(ctx context.Context) error {
	cfg, err := c.AuthOAuthConfig(ctx)
	if err != nil {
		return err
	}
	cmd := &authorizer.Command{
		AuthFlow: "BrowserFlow",
		UsePKCE:  true,
		OAuthConfig: authorizer.OAuthConfig{
			ConfigURL: strings.TrimSpace(cfg.ConfigURL),
		},
	}
	if len(cfg.Scopes) > 0 {
		cmd.Scopes = cfg.Scopes
	} else {
		cmd.Scopes = []string{"openid"}
	}
	tok, err := authorizer.New().Authorize(ctx, cmd)
	if err != nil {
		return err
	}
	if tok == nil {
		return fmt.Errorf("oauth authorize returned empty token")
	}
	idToken, _ := tok.Extra("id_token").(string)
	if strings.TrimSpace(idToken) == "" {
		return fmt.Errorf("id_token missing from oauth response")
	}
	return c.AuthSessionExchange(ctx, idToken)
}

func (c *HTTPClient) AuthOOBLogin(ctx context.Context, configURL, secretsURL string, scopes []string) (*oauth2.Token, error) {
	configURL = strings.TrimSpace(configURL)
	secretsURL = strings.TrimSpace(secretsURL)
	if configURL == "" {
		return nil, fmt.Errorf("configURL is required")
	}
	if secretsURL == "" {
		return nil, fmt.Errorf("secretsURL is required")
	}
	cmd := &authorizer.Command{
		AuthFlow:   "OOB",
		UsePKCE:    true,
		SecretsURL: secretsURL,
		OAuthConfig: authorizer.OAuthConfig{
			ConfigURL: configURL,
		},
	}
	if len(scopes) > 0 {
		cmd.Scopes = scopes
	} else {
		cmd.Scopes = []string{"openid"}
	}
	svc := authorizer.New()
	tok, err := svc.Authorize(ctx, cmd)
	if err != nil {
		return nil, err
	}
	if tok == nil {
		return nil, fmt.Errorf("oauth token was nil")
	}
	var mu sync.Mutex
	bearerFromToken := func(t *oauth2.Token) string {
		if t == nil {
			return ""
		}
		if idToken, _ := t.Extra("id_token").(string); strings.TrimSpace(idToken) != "" {
			return strings.TrimSpace(idToken)
		}
		return strings.TrimSpace(t.AccessToken)
	}
	bearer := bearerFromToken(tok)
	c.tokenProvider = func(reqCtx context.Context) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		if tok != nil && tok.RefreshToken != "" && !tok.Expiry.IsZero() && time.Now().After(tok.Expiry.Add(-10*time.Second)) {
			refreshed, refreshErr := svc.RefreshToken(reqCtx, tok, &authorizer.OAuthConfig{ConfigURL: configURL})
			if refreshErr == nil && refreshed != nil {
				if refreshed.RefreshToken == "" {
					refreshed.RefreshToken = tok.RefreshToken
				}
				tok = refreshed
				bearer = bearerFromToken(tok)
			}
		}
		return bearer, nil
	}
	return tok, nil
}

func (c *HTTPClient) AuthOOBSession(ctx context.Context, secretsURL string, scopes []string) error {
	secretsURL = strings.TrimSpace(secretsURL)
	if secretsURL == "" {
		return fmt.Errorf("secretsURL is required")
	}
	req := &OOBRequest{SecretsURL: secretsURL}
	if len(scopes) > 0 {
		req.Scopes = scopes
	}
	return c.doJSON(ctx, "POST", "/v1/api/auth/oob", req, nil)
}
