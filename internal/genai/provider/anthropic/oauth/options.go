package oauth

// Options configures Anthropic OAuth-based credential acquisition.
type Options struct {
	ClientURL string
	TokensURL string
	Issuer    string
	TokenURL  string
	APIKeyURL string
	Scope     string
}
