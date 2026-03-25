package integrate

import (
	"context"
	"net/http"
	"time"

	mcpclient "github.com/viant/mcp/client"
	"github.com/viant/mcp/client/auth"
	authtransport "github.com/viant/mcp/client/auth/transport"
)

// NewAuthRoundTripper builds an auth RoundTripper configured for BFF exchange and cookie reuse.
func NewAuthRoundTripper(jar http.CookieJar, base http.RoundTripper, rejectTTL time.Duration) (*authtransport.RoundTripper, error) {
	opts := []authtransport.Option{
		authtransport.WithBackendForFrontendAuth(),
		authtransport.WithCookieJar(jar),
	}
	if base == nil {
		base = http.DefaultTransport
	}
	if base != nil {
		opts = append(opts, authtransport.WithTransport(base))
	}
	return authtransport.New(opts...)
}

// NewAuthRoundTripperWithPrompt wraps the provided base transport with an OOB
// prompt trigger and builds the auth RoundTripper on top.
func NewAuthRoundTripperWithPrompt(jar http.CookieJar, base http.RoundTripper, rejectTTL time.Duration, prompt OAuthPrompt) (*authtransport.RoundTripper, error) {
	if prompt != nil {
		base = NewOOBRoundTripper(base, prompt)
	}
	return NewAuthRoundTripper(jar, base, rejectTTL)
}

// NewAuthRoundTripperWithElicitation builds an auth RoundTripper that surfaces
// OAuth authorization URLs via a callback instead of opening a CLI browser.
func NewAuthRoundTripperWithElicitation(jar http.CookieJar, base http.RoundTripper, rejectTTL time.Duration, urlHandler authtransport.AuthURLHandler) (*authtransport.RoundTripper, error) {
	opts := []authtransport.Option{
		authtransport.WithBackendForFrontendAuth(),
	}
	if jar != nil {
		opts = append(opts, authtransport.WithCookieJar(jar))
	}
	if base == nil {
		base = http.DefaultTransport
	}
	opts = append(opts, authtransport.WithTransport(base))
	if urlHandler != nil {
		opts = append(opts, authtransport.WithElicitationAuthFlow(urlHandler))
	}
	return authtransport.New(opts...)
}

// NewClientWithAuthInterceptor attaches an Authorizer that auto-retries once on 401.
func NewClientWithAuthInterceptor(client *mcpclient.Client, rt *authtransport.RoundTripper) *mcpclient.Client {
	if client == nil || rt == nil {
		return client
	}
	authorizer := auth.NewAuthorizer(rt)
	mcpclient.WithAuthInterceptor(authorizer)(client)
	return client
}

// ContextWithAuthToken returns a context that carries a bearer token for the auth RoundTripper.
func ContextWithAuthToken(ctx context.Context, token string) context.Context {
	if token == "" {
		return ctx
	}
	return context.WithValue(ctx, authtransport.ContextAuthTokenKey, token)
}
