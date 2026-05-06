package oauth

import "time"

// TokenState persists Anthropic OAuth tokens and an optionally minted API key.
type TokenState struct {
	AccessToken  string    `json:"access_token,omitempty" yaml:"access_token,omitempty"`
	RefreshToken string    `json:"refresh_token,omitempty" yaml:"refresh_token,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitempty" yaml:"expires_at,omitempty"`
	LastRefresh  time.Time `json:"last_refresh,omitempty" yaml:"last_refresh,omitempty"`
	Scope        string    `json:"scope,omitempty" yaml:"scope,omitempty"`

	AnthropicAPIKey      string    `json:"anthropic_api_key,omitempty" yaml:"anthropic_api_key,omitempty"`
	AnthropicAPIKeyAt    time.Time `json:"anthropic_api_key_at,omitempty" yaml:"anthropic_api_key_at,omitempty"`
	AnthropicAPIKeyTTLMS int64     `json:"anthropic_api_key_ttl_ms,omitempty" yaml:"anthropic_api_key_ttl_ms,omitempty"`
}
