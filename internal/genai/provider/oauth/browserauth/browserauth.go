package browserauth

import (
	"context"
	"fmt"

	"github.com/viant/agently-core/protocol/mcp/auth/integrate"
	"github.com/viant/scy/auth/flow"
	"github.com/viant/scy/auth/flow/endpoint"
)

// Run starts a localhost callback endpoint, opens the authorization URL in the
// system browser, and exchanges the resulting authorization code.
func Run(
	ctx context.Context,
	buildAuthorizeURL func(ctx context.Context, redirectURI, state, codeVerifier string) (string, error),
	exchangeCode func(ctx context.Context, redirectURI, codeVerifier, code string) error,
) error {
	if buildAuthorizeURL == nil {
		return fmt.Errorf("buildAuthorizeURL was nil")
	}
	if exchangeCode == nil {
		return fmt.Errorf("exchangeCode was nil")
	}
	callback, err := endpoint.New()
	if err != nil {
		return fmt.Errorf("failed to create auth callback endpoint: %w", err)
	}
	defer callback.Close()
	go callback.Start()

	redirectURI := callback.RedirectURL()
	codeVerifier := flow.GenerateCodeVerifier()
	state := flow.GenerateCodeVerifier()

	authURL, err := buildAuthorizeURL(ctx, redirectURI, state, codeVerifier)
	if err != nil {
		return err
	}
	if err := (integrate.OSBrowserPrompt{}).PromptOOB(ctx, authURL, integrate.OAuthMeta{}); err != nil {
		return err
	}
	if err := callback.Wait(); err != nil {
		return err
	}
	code := callback.AuthCode()
	if code == "" {
		return fmt.Errorf("oauth browser flow completed without an authorization code")
	}
	return exchangeCode(ctx, redirectURI, codeVerifier, code)
}
