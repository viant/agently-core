package datasource

import "context"

type unboundWindowProvidersKey struct{}

// WithUnboundWindowProviderGuard marks an unbound UI window fetch with the
// host-configured cohort providers. Browser parameters cannot set this value.
// The guard is checked only against a freshly revalidated resource pin.
func WithUnboundWindowProviderGuard(ctx context.Context, providers []string) context.Context {
	if ctx == nil || len(providers) == 0 {
		return ctx
	}
	protected := make(map[string]bool, len(providers))
	for _, provider := range providers {
		if provider != "" {
			protected[provider] = true
		}
	}
	if len(protected) == 0 {
		return ctx
	}
	return context.WithValue(ctx, unboundWindowProvidersKey{}, protected)
}

func unboundWindowProviderProtected(ctx context.Context, provider string) bool {
	protected, _ := ctx.Value(unboundWindowProvidersKey{}).(map[string]bool)
	return provider != "" && protected[provider]
}
