package mcpauth

import "context"

// Blocker pauses a tool invocation while an interactive client completes the
// delegated OAuth flow. Implementations must return only after the same
// invocation may safely continue, or when the interaction is canceled.
type Blocker interface {
	AwaitMCPAuth(context.Context, *LinkRequiredError) error
}

type blockerKey struct{}

// WithBlocker installs the request-scoped interactive OAuth blocker.
func WithBlocker(ctx context.Context, blocker Blocker) context.Context {
	if ctx == nil || blocker == nil {
		return ctx
	}
	return context.WithValue(ctx, blockerKey{}, blocker)
}

// BlockerFromContext returns the request-scoped OAuth blocker, if available.
func BlockerFromContext(ctx context.Context) (Blocker, bool) {
	if ctx == nil {
		return nil, false
	}
	blocker, ok := ctx.Value(blockerKey{}).(Blocker)
	return blocker, ok && blocker != nil
}
