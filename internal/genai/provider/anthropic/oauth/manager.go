package oauth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	browserauth "github.com/viant/agently-core/internal/genai/provider/oauth/browserauth"
	"github.com/viant/scy/auth/flow"
)

const (
	defaultIssuer         = "https://platform.claude.com"
	defaultAuthorizeURL   = "https://claude.com/cai/oauth/authorize"
	defaultTokenURL       = "https://platform.claude.com/v1/oauth/token"
	defaultAPIKeyURL      = "https://api.anthropic.com/api/oauth/claude_cli/create_api_key"
	defaultScope          = "user:profile user:inference"
	defaultAPIKeyCacheTTL = 30 * time.Minute
	defaultRefreshPeriod  = 24 * time.Hour
	defaultRefreshSkew    = 10 * time.Minute
)

type Manager struct {
	issuer          string
	authorizeURL    string
	tokenURL        string
	apiKeyURL       string
	scope           string
	lazyBrowserAuth bool
	httpClient      *http.Client
	client          OAuthClientLoader
	store           TokenStateStore

	mu sync.Mutex
}

var runLazyBrowserAuth = browserauth.Run

func NewManager(options *Options, client OAuthClientLoader, store TokenStateStore, httpClient *http.Client) (*Manager, error) {
	if options == nil {
		return nil, fmt.Errorf("anthropicOAuth options were nil")
	}
	if client == nil {
		return nil, fmt.Errorf("oauth client loader was nil")
	}
	if store == nil {
		return nil, fmt.Errorf("token store was nil")
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	issuer := strings.TrimRight(strings.TrimSpace(options.Issuer), "/")
	if issuer == "" {
		issuer = defaultIssuer
	}
	tokenURL := strings.TrimSpace(options.TokenURL)
	if tokenURL == "" {
		tokenURL = defaultTokenURL
	}
	authorizeURL := strings.TrimSpace(options.AuthorizeURL)
	if authorizeURL == "" {
		authorizeURL = defaultAuthorizeURL
	}
	apiKeyURL := strings.TrimSpace(options.APIKeyURL)
	if apiKeyURL == "" {
		apiKeyURL = defaultAPIKeyURL
	}
	scope := strings.TrimSpace(options.Scope)
	if scope == "" {
		scope = defaultScope
	}
	return &Manager{
		issuer:          issuer,
		authorizeURL:    authorizeURL,
		tokenURL:        tokenURL,
		apiKeyURL:       apiKeyURL,
		scope:           scope,
		lazyBrowserAuth: options.LazyBrowserAuth,
		httpClient:      httpClient,
		client:          client,
		store:           store,
	}, nil
}

// AccessToken returns a refreshed OAuth access token from the persisted token state.
func (m *Manager) AccessToken(ctx context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	oauthClient, err := m.client.Load(ctx)
	if err != nil {
		return "", err
	}
	state, err := m.store.Load(ctx)
	if err != nil {
		if m.shouldLazyAuthorize(err) {
			if _, authErr := m.authorizeInteractively(ctx); authErr != nil {
				return "", authErr
			}
			state, err = m.store.Load(ctx)
		}
	}
	if err != nil {
		return "", err
	}
	if state == nil {
		if m.lazyBrowserAuth {
			if _, authErr := m.authorizeInteractively(ctx); authErr != nil {
				return "", authErr
			}
			state, err = m.store.Load(ctx)
			if err != nil {
				return "", err
			}
		}
	}
	if state == nil {
		return "", fmt.Errorf("token state was empty")
	}
	state, err = m.refreshIfNeeded(ctx, oauthClient, state)
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(state.AccessToken)
	if token == "" {
		return "", fmt.Errorf("access_token is required")
	}
	return token, nil
}

// APIKey returns a Claude API key minted from Anthropic OAuth tokens.
func (m *Manager) APIKey(ctx context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	oauthClient, err := m.client.Load(ctx)
	if err != nil {
		return "", err
	}
	state, err := m.store.Load(ctx)
	if err != nil {
		if m.shouldLazyAuthorize(err) {
			if _, authErr := m.authorizeInteractively(ctx); authErr != nil {
				return "", authErr
			}
			state, err = m.store.Load(ctx)
		}
	}
	if err != nil {
		return "", err
	}
	if state == nil {
		if m.lazyBrowserAuth {
			if _, authErr := m.authorizeInteractively(ctx); authErr != nil {
				return "", authErr
			}
			state, err = m.store.Load(ctx)
			if err != nil {
				return "", err
			}
		}
	}
	if state == nil {
		return "", fmt.Errorf("token state was empty")
	}
	if cached := strings.TrimSpace(state.AnthropicAPIKey); cached != "" && !isAPIKeyExpired(state) {
		return cached, nil
	}
	state, err = m.refreshIfNeeded(ctx, oauthClient, state)
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(state.AccessToken)
	if token == "" {
		return "", fmt.Errorf("access_token is required to mint API key")
	}
	apiKey, err := m.obtainAPIKey(ctx, token)
	if err != nil {
		return "", err
	}
	state.AnthropicAPIKey = apiKey
	state.AnthropicAPIKeyAt = time.Now().UTC()
	if state.AnthropicAPIKeyTTLMS == 0 {
		state.AnthropicAPIKeyTTLMS = defaultAPIKeyCacheTTL.Milliseconds()
	}
	if err := m.store.Save(ctx, state); err != nil {
		return "", err
	}
	return apiKey, nil
}

func (m *Manager) refreshIfNeeded(ctx context.Context, oauthClient *OAuthClientConfig, state *TokenState) (*TokenState, error) {
	if state == nil {
		return nil, fmt.Errorf("token state was nil")
	}
	if strings.TrimSpace(state.RefreshToken) == "" {
		return state, nil
	}
	if !needsRefresh(state) {
		return state, nil
	}
	return m.refreshOnce(ctx, oauthClient, state)
}

func (m *Manager) refreshOnce(ctx context.Context, oauthClient *OAuthClientConfig, state *TokenState) (*TokenState, error) {
	payload := map[string]any{
		"grant_type":    "refresh_token",
		"refresh_token": state.RefreshToken,
		"client_id":     oauthClient.ClientID,
		"scope":         m.scope,
	}
	if secret := strings.TrimSpace(oauthClient.ClientSecret); secret != "" {
		payload["client_secret"] = secret
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.tokenURLForClient(oauthClient), bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("refresh token endpoint returned status %d: %s", resp.StatusCode, string(body))
	}
	var parsed struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
		Scope        string `json:"scope"`
		TokenType    string `json:"token_type"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}
	if token := strings.TrimSpace(parsed.AccessToken); token != "" {
		state.AccessToken = token
	}
	if token := strings.TrimSpace(parsed.RefreshToken); token != "" {
		state.RefreshToken = token
	}
	if parsed.ExpiresIn > 0 {
		state.ExpiresAt = time.Now().UTC().Add(time.Duration(parsed.ExpiresIn) * time.Second)
	}
	if scope := strings.TrimSpace(parsed.Scope); scope != "" {
		state.Scope = scope
	}
	state.LastRefresh = time.Now().UTC()
	state.AnthropicAPIKey = ""
	if err := m.store.Save(ctx, state); err != nil {
		return nil, err
	}
	return state, nil
}

func (m *Manager) obtainAPIKey(ctx context.Context, accessToken string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.apiKeyURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", fmt.Errorf("api key creation failed with status %d: %s", resp.StatusCode, string(body))
	}
	var parsed struct {
		RawKey string `json:"raw_key"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", err
	}
	if strings.TrimSpace(parsed.RawKey) == "" {
		return "", fmt.Errorf("api key creation returned empty raw_key")
	}
	return parsed.RawKey, nil
}

func (m *Manager) tokenURLForClient(client *OAuthClientConfig) string {
	if client != nil && strings.TrimSpace(client.TokenURL) != "" {
		return strings.TrimSpace(client.TokenURL)
	}
	return m.tokenURL
}

func needsRefresh(state *TokenState) bool {
	if state == nil {
		return false
	}
	if !state.ExpiresAt.IsZero() && time.Until(state.ExpiresAt) <= defaultRefreshSkew {
		return true
	}
	if state.LastRefresh.IsZero() {
		return true
	}
	if time.Since(state.LastRefresh) >= defaultRefreshPeriod {
		return true
	}
	if exp, ok := parseJWTExpiry(state.AccessToken); ok && time.Until(exp) <= defaultRefreshSkew {
		return true
	}
	return false
}

func isAPIKeyExpired(state *TokenState) bool {
	if state == nil {
		return true
	}
	ttl := defaultAPIKeyCacheTTL
	if state.AnthropicAPIKeyTTLMS > 0 {
		ttl = time.Duration(state.AnthropicAPIKeyTTLMS) * time.Millisecond
	}
	if state.AnthropicAPIKeyAt.IsZero() {
		return true
	}
	return time.Since(state.AnthropicAPIKeyAt) >= ttl
}

// BuildAuthorizeURL builds the Anthropic OAuth authorization URL for
// subscriber-style Claude.ai login using Code+PKCE.
func (m *Manager) BuildAuthorizeURL(ctx context.Context, redirectURI string, state string, codeVerifier string) (string, error) {
	oauthClient, err := m.client.Load(ctx)
	if err != nil {
		return "", err
	}
	redirectURI = strings.TrimSpace(redirectURI)
	if redirectURI == "" {
		return "", fmt.Errorf("redirectURI was empty")
	}
	codeVerifier = strings.TrimSpace(codeVerifier)
	if codeVerifier == "" {
		return "", fmt.Errorf("codeVerifier was empty")
	}
	codeChallenge := flow.GenerateCodeChallenge(codeVerifier)
	params := [][2]string{
		{"client_id", oauthClient.ClientID},
		{"response_type", "code"},
		{"redirect_uri", redirectURI},
		{"scope", m.scope},
		{"code_challenge", codeChallenge},
		{"code_challenge_method", "S256"},
		{"state", state},
		{"code", "true"},
	}
	qs := make([]string, 0, len(params))
	for _, kv := range params {
		qs = append(qs, kv[0]+"="+escapeQueryValue(kv[1]))
	}
	return m.authorizeURL + "?" + strings.Join(qs, "&"), nil
}

// ExchangeAuthorizationCode exchanges an Anthropic OAuth authorization code for
// tokens and persists them.
func (m *Manager) ExchangeAuthorizationCode(ctx context.Context, redirectURI string, codeVerifier string, code string) (*TokenState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.exchangeAuthorizationCode(ctx, redirectURI, codeVerifier, code)
}

func (m *Manager) exchangeAuthorizationCode(ctx context.Context, redirectURI string, codeVerifier string, code string) (*TokenState, error) {
	oauthClient, err := m.client.Load(ctx)
	if err != nil {
		return nil, err
	}
	redirectURI = strings.TrimSpace(redirectURI)
	if redirectURI == "" {
		return nil, fmt.Errorf("redirectURI was empty")
	}
	codeVerifier = strings.TrimSpace(codeVerifier)
	if codeVerifier == "" {
		return nil, fmt.Errorf("codeVerifier was empty")
	}
	code = strings.TrimSpace(code)
	if code == "" {
		return nil, fmt.Errorf("authorization code was empty")
	}

	tokens, err := m.exchangeCodeForTokens(ctx, oauthClient, redirectURI, codeVerifier, code)
	if err != nil {
		return nil, err
	}
	state := &TokenState{
		AccessToken:  tokens.AccessToken,
		RefreshToken: tokens.RefreshToken,
		LastRefresh:  time.Now().UTC(),
		Scope:        tokens.Scope,
	}
	if tokens.ExpiresIn > 0 {
		state.ExpiresAt = time.Now().UTC().Add(time.Duration(tokens.ExpiresIn) * time.Second)
	}
	if err := m.store.Save(ctx, state); err != nil {
		return nil, err
	}
	return state, nil
}

type exchangedTokens struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    int64
	Scope        string
}

func (m *Manager) exchangeCodeForTokens(ctx context.Context, oauthClient *OAuthClientConfig, redirectURI string, codeVerifier string, code string) (*exchangedTokens, error) {
	payload := map[string]any{
		"grant_type":    "authorization_code",
		"code":          code,
		"redirect_uri":  redirectURI,
		"client_id":     oauthClient.ClientID,
		"code_verifier": codeVerifier,
	}
	if secret := strings.TrimSpace(oauthClient.ClientSecret); secret != "" {
		payload["client_secret"] = secret
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.tokenURLForClient(oauthClient), bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("token endpoint returned status %d: %s", resp.StatusCode, string(body))
	}
	var parsed struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
		Scope        string `json:"scope"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}
	return &exchangedTokens{
		AccessToken:  parsed.AccessToken,
		RefreshToken: parsed.RefreshToken,
		ExpiresIn:    parsed.ExpiresIn,
		Scope:        parsed.Scope,
	}, nil
}

func escapeQueryValue(value string) string {
	escaped := url.QueryEscape(value)
	return strings.ReplaceAll(escaped, "+", "%20")
}

func (m *Manager) shouldLazyAuthorize(err error) bool {
	if !m.lazyBrowserAuth || err == nil {
		return false
	}
	_, ok := err.(*TokenStateNotFoundError)
	return ok
}

func (m *Manager) authorizeInteractively(ctx context.Context) (*TokenState, error) {
	var state *TokenState
	err := runLazyBrowserAuth(
		ctx,
		func(ctx context.Context, redirectURI, authState, codeVerifier string) (string, error) {
			return m.BuildAuthorizeURL(ctx, redirectURI, authState, codeVerifier)
		},
		func(ctx context.Context, redirectURI, codeVerifier, code string) error {
			var err error
			state, err = m.exchangeAuthorizationCode(ctx, redirectURI, codeVerifier, code)
			return err
		},
	)
	if err != nil {
		return nil, err
	}
	return state, nil
}
