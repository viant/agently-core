package memory

import (
	"context"

	runtimerecovery "github.com/viant/agently-core/runtime/recovery"
)

// Deprecated: package memory is a compatibility shim over runtime/recovery.
// New code should import runtime/recovery directly.
const (
	ContextRecoveryCompact      = runtimerecovery.ModeCompact
	ContextRecoveryPruneCompact = runtimerecovery.ModePruneCompact
)

func WithContextRecoveryMode(ctx context.Context, mode string) context.Context {
	return runtimerecovery.WithMode(ctx, mode)
}

func ContextRecoveryModeFromContext(ctx context.Context) (string, bool) {
	return runtimerecovery.ModeFromContext(ctx)
}
