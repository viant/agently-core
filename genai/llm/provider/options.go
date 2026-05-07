package provider

import basecfg "github.com/viant/agently-core/genai/llm/provider/base"

// ChatGPTOAuthOptions configures ChatGPT OAuth (Code+PKCE) based credential acquisition.
// This is intended for flows that mint an OpenAI API key (or equivalent) via token exchange.
type ChatGPTOAuthOptions struct {
	// OAuth client configuration (client id / secret), stored as a scy resource.
	// Typically points to a secret containing `cred.Oauth2Config`.
	ClientURL string `yaml:"clientURL,omitempty" json:"clientURL,omitempty"`

	// Token state persistence location, stored as a scy resource.
	// Intended to store refresh/access/id tokens and related metadata.
	TokensURL string `yaml:"tokensURL,omitempty" json:"tokensURL,omitempty"`

	// OAuth issuer, e.g. "https://auth.openai.com".
	Issuer string `yaml:"issuer,omitempty" json:"issuer,omitempty"`

	// Optional restriction for which ChatGPT workspace/account may be used.
	AllowedWorkspaceID string `yaml:"allowedWorkspaceID,omitempty" json:"allowedWorkspaceID,omitempty"`

	// When true, if API-key minting fails, provider may use OAuth access_token as Bearer.
	// Useful for ChatGPT-backend style flows.
	UseAccessTokenFallback bool `yaml:"useAccessTokenFallback,omitempty" json:"useAccessTokenFallback,omitempty"`

	// When true, and no token state exists yet, start an interactive browser
	// OAuth flow on first use and persist the resulting tokens into TokensURL.
	LazyBrowserAuth bool `yaml:"lazyBrowserAuth,omitempty" json:"lazyBrowserAuth,omitempty"`
}

// AnthropicOAuthOptions configures Anthropic OAuth credential acquisition for
// Claude.ai subscriber-style Bearer auth and optional API key minting.
type AnthropicOAuthOptions struct {
	// OAuth client configuration (client id / secret), stored as a scy resource.
	ClientURL string `yaml:"clientURL,omitempty" json:"clientURL,omitempty"`

	// Token state persistence location, stored as a scy resource.
	TokensURL string `yaml:"tokensURL,omitempty" json:"tokensURL,omitempty"`

	// Optional OAuth issuer base (defaults to https://platform.claude.com).
	Issuer string `yaml:"issuer,omitempty" json:"issuer,omitempty"`

	// Optional authorize endpoint override. Defaults to
	// https://claude.com/cai/oauth/authorize for subscriber-style login.
	AuthorizeURL string `yaml:"authorizeURL,omitempty" json:"authorizeURL,omitempty"`

	// Optional token endpoint override. Defaults to <issuer>/v1/oauth/token.
	TokenURL string `yaml:"tokenURL,omitempty" json:"tokenURL,omitempty"`

	// Optional API key mint endpoint override. Defaults to
	// https://api.anthropic.com/api/oauth/claude_cli/create_api_key.
	APIKeyURL string `yaml:"apiKeyURL,omitempty" json:"apiKeyURL,omitempty"`

	// Optional refresh scope override. Defaults to "user:profile user:inference".
	Scope string `yaml:"scope,omitempty" json:"scope,omitempty"`

	// When true, the provider prefers a minted API key from OAuth refresh state
	// over direct Bearer auth. This mirrors the ChatGPT/OpenAI API-key path;
	// leave false to use the Claude.ai subscriber endpoint with Bearer auth.
	UseAPIKeyExchange bool `yaml:"useAPIKeyExchange,omitempty" json:"useAPIKeyExchange,omitempty"`

	// When true, and no token state exists yet, start an interactive browser
	// OAuth flow on first use and persist the resulting tokens into TokensURL.
	LazyBrowserAuth bool `yaml:"lazyBrowserAuth,omitempty" json:"lazyBrowserAuth,omitempty"`
}

type Options struct {
	Model             string                 `yaml:"model,omitempty" json:"model,omitempty"`
	Provider          string                 `yaml:"provider,omitempty" json:"provider,omitempty"`
	APIKeyURL         string                 `yaml:"apiKeyURL,omitempty" json:"APIKeyURL,omitempty"`
	EnvKey            string                 `yaml:"envKey,omitempty" json:"envKey,omitempty"` // environment variable key to use for API key
	CredentialsURL    string                 `yaml:"credentialsURL,omitempty" json:"credentialsURL,omitempty"`
	URL               string                 `yaml:"url,omitempty" json:"url,omitempty"`
	ProjectID         string                 `yaml:"projectID,omitempty" json:"projectID,omitempty"`
	Temperature       *float64               `yaml:"temperature,omitempty" json:"temperature,omitempty"`
	MaxTokens         int                    `yaml:"maxTokens,omitempty" json:"maxTokens,omitempty"`
	TopP              float64                `yaml:"topP,omitempty" json:"topP,omitempty"`
	UserAgent         string                 `yaml:"userAgent,omitempty" json:"userAgent,omitempty"`
	Originator        string                 `yaml:"originator,omitempty" json:"originator,omitempty"`
	CodexBetaFeatures string                 `yaml:"codexBetaFeatures,omitempty" json:"codexBetaFeatures,omitempty"`
	OpenAILogging     *bool                  `yaml:"openaiLogging,omitempty" json:"openaiLogging,omitempty"`
	Meta              map[string]interface{} `yaml:"meta,omitempty" json:"meta,omitempty"`
	Region            string                 `yaml:"region,omitempty" json:"region,omitempty"`
	UsageListener     basecfg.UsageListener  `yaml:"-" json:"-"`

	// ---- Pricing ----
	// Cost per 1,000 input tokens in USD (or chosen currency). Optional.
	InputTokenPrice float64 `yaml:"inputTokenPrice,omitempty" json:"inputTokenPrice,omitempty"`
	// Cost per 1,000 output/completion tokens.
	OutputTokenPrice float64 `yaml:"outputTokenPrice,omitempty" json:"outputTokenPrice,omitempty"`
	// Cost per 1,000 tokens served from cache (no LLM call).
	CachedTokenPrice float64 `yaml:"cachedTokenPrice,omitempty" json:"cachedTokenPrice,omitempty"`

	// Preview limit for tool results when this model is used (bytes).
	ToolResultPreviewLimit int `yaml:"toolResultPreviewLimit,omitempty" json:"toolResultPreviewLimit,omitempty"`

	// ---- Safety Limits ----
	// SafeEffectiveInputTokens defines a conservative safe input token count
	// (excludes model output and provider overhead). Intended for request planning.
	SafeEffectiveInputTokens int `yaml:"safeEffectiveInputTokens,omitempty" json:"safeEffectiveInputTokens,omitempty"`

	// ContextContinuation explicitly enables/disables provider continuation
	// for models (i.e. via previous_response_id for openai).
	ContextContinuation *bool `json:"contextContinuation,omitempty" yaml:"contextContinuation,omitempty"`

	EnableContinuationFormat bool `json:"enableContinuationFormat,omitempty" yaml:"enableContinuationFormat,omitempty"`

	// ChatGPTOAuth configures ChatGPT OAuth based credential acquisition.
	ChatGPTOAuth *ChatGPTOAuthOptions `yaml:"chatgptOAuth,omitempty" json:"chatgptOAuth,omitempty"`

	// AnthropicOAuth configures Anthropic OAuth based credential acquisition.
	AnthropicOAuth *AnthropicOAuthOptions `yaml:"anthropicOAuth,omitempty" json:"anthropicOAuth,omitempty"`
}
