package sdk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"

	"github.com/viant/scy"
	"github.com/viant/scy/auth/authorizer"
	"github.com/viant/scy/auth/flow"
	"github.com/viant/scy/cred"
	"golang.org/x/oauth2"
)

const mcpAuthCSRFHeader = "X-Agently-Csrf"

// MCPAuthStatus describes the delegated OAuth connection for one MCP server.
type MCPAuthStatus struct {
	Server    string   `json:"server"`
	Provider  string   `json:"provider,omitempty"`
	Connected bool     `json:"connected"`
	Scopes    []string `json:"scopes,omitempty"`
	ExpiresAt string   `json:"expiresAt,omitempty"`
	Pending   bool     `json:"pending,omitempty"`
	CSRFToken string   `json:"csrfToken,omitempty"`
}

// MCPOOBLinkOptions configures a local out-of-band provider login. Workspace
// authentication remains on the HTTPClient cookie jar; the provider grant is
// obtained independently and then submitted for server-side validation and
// canonical-user storage.
type MCPOOBLinkOptions struct {
	Server     string
	ConfigURL  string
	SecretsURL string
	Scopes     []string
	Resource   string
}

// MCPAuthStatus returns the delegated connection state for an MCP server.
func (c *HTTPClient) MCPAuthStatus(ctx context.Context, server string) (*MCPAuthStatus, error) {
	server = strings.TrimSpace(server)
	if server == "" {
		return nil, fmt.Errorf("mcp server is required")
	}
	var result MCPAuthStatus
	if err := c.doJSON(ctx, http.MethodGet, "/v1/api/auth/mcp/"+url.PathEscape(server)+"/status", nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// AuthMCPOOBLink performs provider OOB authorization locally and links the
// resulting grant to the currently authenticated canonical workspace user.
// The server validates the untrusted grant before encrypted persistence.
func (c *HTTPClient) AuthMCPOOBLink(ctx context.Context, opts *MCPOOBLinkOptions) error {
	if opts == nil {
		return fmt.Errorf("mcp OOB options are required")
	}
	server := strings.TrimSpace(opts.Server)
	configURL := strings.TrimSpace(opts.ConfigURL)
	secretsURL := strings.TrimSpace(opts.SecretsURL)
	if server == "" {
		return fmt.Errorf("mcp server is required")
	}
	if configURL == "" {
		return fmt.Errorf("mcp oauth configURL is required")
	}
	if secretsURL == "" {
		return fmt.Errorf("mcp OOB secretsURL is required")
	}

	status, err := c.MCPAuthStatus(ctx, server)
	if err != nil {
		return fmt.Errorf("load mcp auth status: %w", err)
	}
	if status.Connected {
		return nil
	}
	if strings.TrimSpace(status.CSRFToken) == "" {
		return fmt.Errorf("mcp auth status returned no CSRF token")
	}

	oauthCfg := authorizer.OAuthConfig{ConfigURL: configURL}
	authz := authorizer.New()
	if err := authz.EnsureConfig(ctx, &oauthCfg); err != nil {
		return fmt.Errorf("load mcp oauth config: %w", err)
	}
	if oauthCfg.Config != nil && len(opts.Scopes) > 0 {
		clone := *oauthCfg.Config
		clone.Scopes = append([]string(nil), opts.Scopes...)
		oauthCfg.Config = &clone
	}
	token, err := authorizeMCPOOB(ctx, oauthCfg.Config, secretsURL, opts.Scopes, strings.TrimSpace(opts.Resource))
	if err != nil {
		return fmt.Errorf("mcp provider OOB authorization: %w", err)
	}
	if token == nil || strings.TrimSpace(token.AccessToken) == "" {
		return fmt.Errorf("mcp provider OOB authorization returned no access token")
	}
	payload := map[string]interface{}{
		"accessToken": strings.TrimSpace(token.AccessToken),
	}
	if value := strings.TrimSpace(token.RefreshToken); value != "" {
		payload["refreshToken"] = value
	}
	if value := strings.TrimSpace(token.TokenType); value != "" {
		payload["tokenType"] = value
	}
	if !token.Expiry.IsZero() {
		payload["expiresAt"] = token.Expiry.UTC().Format("2006-01-02T15:04:05Z07:00")
	}
	if idToken, _ := token.Extra("id_token").(string); strings.TrimSpace(idToken) != "" {
		payload["idToken"] = strings.TrimSpace(idToken)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := c.newRequest(ctx, http.MethodPost, "/v1/api/auth/mcp/"+url.PathEscape(server)+"/oob", bytes.NewReader(body), "application/json")
	if err != nil {
		return err
	}
	req.Header.Set(mcpAuthCSRFHeader, status.CSRFToken)
	response, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("mcp OOB link failed: %s: %s", response.Status, strings.TrimSpace(string(data)))
	}
	return nil
}

// authorizeMCPOOB is the CLI/headless authorization-code adapter. Unlike the
// legacy SCY OOB helper, it honors the OAuth client's registered RedirectURL;
// this lets a dedicated MCP client use its real Agently callback registration
// while the CLI captures (rather than follows) the provider redirect.
func authorizeMCPOOB(ctx context.Context, config *oauth2.Config, secretsURL string, scopes []string, oauthResource string) (*oauth2.Token, error) {
	if config == nil {
		return nil, fmt.Errorf("mcp oauth config is unavailable")
	}
	redirectURL := strings.TrimSpace(config.RedirectURL)
	if redirectURL == "" {
		return nil, fmt.Errorf("mcp oauth client has no registered redirectURL")
	}
	resource := scy.EncodedResource(secretsURL).Decode(ctx, reflect.TypeOf(cred.Basic{}))
	if resource == nil {
		return nil, fmt.Errorf("mcp OOB secrets resource is invalid")
	}
	secret, err := scy.New().Load(ctx, resource)
	if err != nil {
		return nil, fmt.Errorf("load mcp OOB credentials: %w", err)
	}
	basic, ok := secret.Target.(*cred.Basic)
	if !ok || strings.TrimSpace(basic.Username) == "" || basic.Password == "" {
		return nil, fmt.Errorf("mcp OOB credentials are not basic credentials")
	}

	verifier := flow.GenerateCodeVerifier()
	state := flow.GenerateCodeVerifier()
	authOptions := []flow.Option{
		flow.WithPKCE(true),
		flow.WithCodeVerifier(verifier),
		flow.WithState(state),
		flow.WithRedirectURI(redirectURL),
		flow.WithScopes(scopes...),
	}
	if oauthResource != "" {
		authOptions = append(authOptions, flow.WithAuthURLParam("resource", oauthResource))
	}
	authURL, err := flow.BuildAuthCodeURL(config, authOptions...)
	if err != nil {
		return nil, err
	}
	form := url.Values{"username": {basic.Username}, "password": {basic.Password}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, authURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	providerClient := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := providerClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	if response.StatusCode < 300 || response.StatusCode >= 400 {
		return nil, fmt.Errorf("provider authorization returned status %d", response.StatusCode)
	}
	location, err := url.Parse(response.Header.Get("Location"))
	if err != nil {
		return nil, fmt.Errorf("provider redirect is invalid: %w", err)
	}
	expectedRedirect, err := url.Parse(redirectURL)
	if err != nil || !strings.EqualFold(location.Scheme, expectedRedirect.Scheme) || !strings.EqualFold(location.Host, expectedRedirect.Host) || location.EscapedPath() != expectedRedirect.EscapedPath() {
		return nil, fmt.Errorf("provider returned an unexpected redirect target")
	}
	if returnedState := strings.TrimSpace(location.Query().Get("state")); returnedState == "" || returnedState != state {
		return nil, fmt.Errorf("provider redirect state validation failed")
	}
	if oauthErr := strings.TrimSpace(location.Query().Get("error")); oauthErr != "" {
		return nil, fmt.Errorf("provider authorization rejected the request: %s", oauthErr)
	}
	code := strings.TrimSpace(location.Query().Get("code"))
	if code == "" {
		return nil, fmt.Errorf("provider redirect carried no authorization code")
	}
	exchangeOptions := []oauth2.AuthCodeOption{
		oauth2.SetAuthURLParam("redirect_uri", redirectURL),
		oauth2.SetAuthURLParam("code_verifier", verifier),
	}
	if oauthResource != "" {
		exchangeOptions = append(exchangeOptions, oauth2.SetAuthURLParam("resource", oauthResource))
	}
	return config.Exchange(ctx, code, exchangeOptions...)
}
