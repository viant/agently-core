package memory

import runtimeprojection "github.com/viant/agently-core/runtime/projection"

// Deprecated: package memory is a compatibility shim over runtime/projection.
// New code should import runtime/projection directly.
// ContextProjection is a compatibility alias for runtime/projection.ContextProjection.
type ContextProjection = runtimeprojection.ContextProjection

// ProjectionState is a compatibility alias for runtime/projection.ProjectionState.
type ProjectionState = runtimeprojection.ProjectionState
