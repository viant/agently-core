package anthropic

import (
	"context"

	basecfg "github.com/viant/agently-core/genai/llm/provider/base"
)

type APIKeyProvider func(ctx context.Context) (string, error)
type AuthTokenProvider func(ctx context.Context) (string, error)

// ClientOption mutates the Anthropic client.
type ClientOption func(*Client)

func WithBaseURL(baseURL string) ClientOption {
	return func(c *Client) { basecfg.WithBaseURL(baseURL)(&c.Config) }
}

func WithMaxTokens(maxTokens int) ClientOption {
	return func(c *Client) { c.MaxTokens = maxTokens }
}

func WithTemperature(temp float64) ClientOption {
	return func(c *Client) { c.Temperature = &temp }
}

func WithUsageListener(l basecfg.UsageListener) ClientOption {
	return func(c *Client) { c.UsageListener = l }
}

func WithAPIKeyProvider(provider APIKeyProvider) ClientOption {
	return func(c *Client) { c.APIKeyProvider = provider }
}

func WithAuthTokenProvider(provider AuthTokenProvider) ClientOption {
	return func(c *Client) { c.AuthTokenProvider = provider }
}

func WithAuthToken(token string) ClientOption {
	return func(c *Client) { c.AuthToken = token }
}

func WithAPIVersion(version string) ClientOption {
	return func(c *Client) {
		if version != "" {
			c.APIVersion = version
		}
	}
}

func WithOAuthBeta(beta string) ClientOption {
	return func(c *Client) {
		if beta != "" {
			c.OAuthBeta = beta
		}
	}
}

func WithAuthSource(source string) ClientOption {
	return func(c *Client) { c.AuthSource = source }
}
