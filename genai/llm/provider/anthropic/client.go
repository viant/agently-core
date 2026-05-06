package anthropic

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	basecfg "github.com/viant/agently-core/genai/llm/provider/base"
)

const (
	defaultBaseURL    = "https://api.anthropic.com"
	defaultAPIVersion = "2023-06-01"
	defaultOAuthBeta  = "oauth-2025-04-20"
	defaultTimeout    = 10 * time.Minute
)

// Client represents a direct Anthropic Claude API client.
type Client struct {
	basecfg.Config

	APIKey            string
	APIKeyProvider    APIKeyProvider
	AuthToken         string
	AuthTokenProvider AuthTokenProvider

	APIVersion string
	OAuthBeta  string
	AuthSource string

	MaxTokens   int
	Temperature *float64
}

func NewClient(apiKey, model string, options ...ClientOption) *Client {
	client := &Client{
		Config: basecfg.Config{
			HTTPClient: &http.Client{Timeout: defaultTimeout},
			BaseURL:    defaultBaseURL,
			Model:      model,
		},
		APIKey:     apiKey,
		APIVersion: defaultAPIVersion,
		OAuthBeta:  defaultOAuthBeta,
	}
	for _, option := range options {
		option(client)
	}
	if client.APIKey == "" && client.APIKeyProvider == nil && client.AuthToken == "" && client.AuthTokenProvider == nil {
		client.APIKey = os.Getenv("ANTHROPIC_API_KEY")
	}
	if client.BaseURL == "" {
		client.BaseURL = defaultBaseURL
	}
	if client.HTTPClient == nil {
		client.HTTPClient = &http.Client{Timeout: defaultTimeout}
	}
	return client
}

func (c *Client) apiKey(ctx context.Context) (string, error) {
	if c.APIKey != "" {
		return c.APIKey, nil
	}
	if c.APIKeyProvider == nil {
		return "", fmt.Errorf("API key is required")
	}
	key, err := c.APIKeyProvider(ctx)
	if err != nil {
		return "", err
	}
	if key == "" {
		return "", fmt.Errorf("API key is required")
	}
	return key, nil
}

func (c *Client) authToken(ctx context.Context) (string, error) {
	if c.AuthToken != "" {
		return c.AuthToken, nil
	}
	if c.AuthTokenProvider == nil {
		return "", nil
	}
	return c.AuthTokenProvider(ctx)
}
