package manager

import (
	"context"
	"strings"

	authctx "github.com/viant/agently-core/internal/auth"
	mcpauth "github.com/viant/agently-core/internal/auth/mcpauth"
	token "github.com/viant/agently-core/internal/auth/token"
	cfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/workspace"
	fsstore "github.com/viant/agently-core/workspace/store/fs"
	shared "github.com/viant/forge/backend/clientpool"
	authcfg "github.com/viant/mcp/client/auth/config"
)

type (
	Manager                 = shared.Manager
	Provider                = shared.Provider
	Option                  = shared.Option
	JarProvider             = shared.JarProvider
	AuthRTProvider          = shared.AuthRTProvider
	UserIDExtractor         = shared.UserIDExtractor
	VisibleCatalogPartition = shared.VisibleCatalogPartition
	SkillsClient            = shared.SkillsClient
	AuthBindingStore        = shared.AuthBindingStore
	MCPAuthBinding          = shared.MCPAuthBinding
)

var (
	ErrBrowserExecutionRequired  = shared.ErrBrowserExecutionRequired
	WithTTL                      = shared.WithTTL
	WithHandlerFactory           = shared.WithHandlerFactory
	WithClientFactory            = shared.WithClientFactory
	WithCookieJar                = shared.WithCookieJar
	WithCookieJarProvider        = shared.WithCookieJarProvider
	WithAuthRoundTripper         = shared.WithAuthRoundTripper
	WithAuthRoundTripperProvider = shared.WithAuthRoundTripperProvider
	WithUserIDExtractor          = shared.WithUserIDExtractor
	WithCredentialResolver       = shared.WithCredentialResolver
	WithProviderRegistry         = shared.WithProviderRegistry
	WithAuthBindingStore         = shared.WithAuthBindingStore
	WithToolCatalogReadSnapshot  = shared.WithToolCatalogReadSnapshot
)

// New keeps application state selection in Core while returning the one shared
// pool instance. No manager or provider is reconstructed for an operation.
func New(provider Provider, options ...Option) (*Manager, error) {
	defaults := []Option{shared.WithAuthBindingStore(NewAuthBindingStore(nil)), shared.WithCredentialErrorHandler(hostCredentialError)}
	return shared.New(provider, append(defaults, options...)...)
}
func NewAuthBindingStore(state workspace.StateStore) *AuthBindingStore {
	if state == nil {
		state = fsstore.NewStateStore("")
	}
	return shared.NewAuthBindingStore(state)
}
func WithTokenProvider(provider token.Provider) Option {
	return shared.WithTokenContextPreparer(func(ctx context.Context, _ string, useID bool) context.Context {
		if provider == nil {
			return ctx
		}
		user := strings.TrimSpace(authctx.EffectiveUserID(ctx))
		if user == "" {
			return ctx
		}
		issuer := strings.TrimSpace(authctx.Provider(ctx))
		if issuer == "" {
			issuer = "oauth"
		}
		updated, err := provider.EnsureTokens(ctx, token.Key{Subject: user, Provider: issuer})
		if err == nil && updated != nil && strings.TrimSpace(authctx.MCPAuthToken(updated, useID)) != "" {
			return updated
		}
		return ctx
	})
}
func hostCredentialError(ctx context.Context, options *cfg.MCPClient, requirement authcfg.Requirement, err error) error {
	wrapped := mcpauth.WrapError(err)
	if required, ok := mcpauth.FromError(wrapped); ok && options != nil && options.Auth != nil && options.Auth.ProviderRegistry != nil {
		if provider, providerErr := options.Auth.ProviderRegistry.ResolveProvider(ctx, requirement.ProviderRef); providerErr == nil && provider != nil {
			if client, _, clientErr := provider.Client(requirement.ClientRef); clientErr == nil && client != nil {
				if timeout, timeoutErr := client.AuthTimeoutDuration(); timeoutErr == nil {
					required.AuthTimeout = timeout
				}
			}
		}
	}
	return wrapped
}
