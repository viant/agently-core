package async

import "context"

type managerContextKey struct{}

// WithManager attaches the async Manager to ctx. Intended for use by the
// runtime bootstrap layer so protocol-layer tools can retrieve the manager
// without importing the higher-level service shared/toolexec package.
//
// Callers that also populate toolexec.WithAsyncManager should call both so
// the two context surfaces stay in sync — the alternative is a single
// canonical context key, which is the longer-term direction.
func WithManager(ctx context.Context, manager *Manager) context.Context {
	if manager == nil {
		return ctx
	}
	return context.WithValue(ctx, managerContextKey{}, manager)
}

// ManagerFromContext returns the *Manager attached to ctx by WithManager.
// Returns (nil, false) when no manager is present or the stored value is
// not of the expected concrete type.
func ManagerFromContext(ctx context.Context) (*Manager, bool) {
	if ctx == nil {
		return nil, false
	}
	manager, ok := ctx.Value(managerContextKey{}).(*Manager)
	return manager, ok && manager != nil
}
