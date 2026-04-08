package memory

import (
	"context"

	runtimeprojection "github.com/viant/agently-core/runtime/projection"
)

// Deprecated: package memory is a compatibility shim over runtime/projection.
// New code should import runtime/projection directly.
// ContextProjection is a compatibility alias for runtime/projection.ContextProjection.
type ContextProjection = runtimeprojection.ContextProjection

// ProjectionState is a compatibility alias for runtime/projection.ProjectionState.
type ProjectionState = runtimeprojection.ProjectionState

// WithProjectionState ensures a mutable ProjectionState exists in context.
func WithProjectionState(ctx context.Context) context.Context {
	return runtimeprojection.WithState(ctx)
}

// ProjectionStateFromContext returns the mutable state holder when present.
func ProjectionStateFromContext(ctx context.Context) (*ProjectionState, bool) {
	return runtimeprojection.StateFromContext(ctx)
}

// ProjectionSnapshotFromContext returns a copy of the current projection value.
func ProjectionSnapshotFromContext(ctx context.Context) (ContextProjection, bool) {
	return runtimeprojection.SnapshotFromContext(ctx)
}
