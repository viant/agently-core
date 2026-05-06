package oauth

import "fmt"

// TokenStateNotFoundError is returned when persisted Anthropic OAuth token
// state cannot be found at the configured storage URL.
type TokenStateNotFoundError struct {
	TokensURL string
}

func (e *TokenStateNotFoundError) Error() string {
	if e == nil {
		return "no Anthropic OAuth token state found"
	}
	if e.TokensURL == "" {
		return "no Anthropic OAuth token state found"
	}
	return fmt.Sprintf("no Anthropic OAuth token state found at %s", e.TokensURL)
}
