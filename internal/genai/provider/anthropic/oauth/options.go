package oauth

// Options configures Anthropic OAuth-based credential acquisition.
type Options struct {
	ClientURL       string
	TokensURL       string
	Issuer          string
	AuthorizeURL    string
	TokenURL        string
	APIKeyURL       string
	Scope           string
	LazyBrowserAuth bool
}
