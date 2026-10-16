package tool

import "context"

// Keep registry catalog identities separate from transport connection keys.
// The shared manager owns user isolation and may reuse the gateway connection.
func (r *Registry) metadataConnectionScope(ctx context.Context, scope, server string) string {
	if owner, ok := r.mgr.(interface {
		MetadataConnectionScope(context.Context, string, string) string
	}); ok {
		return owner.MetadataConnectionScope(ctx, scope, server)
	}
	return scope
}
