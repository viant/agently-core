package memory

import (
	"context"

	runtimediscovery "github.com/viant/agently-core/runtime/discovery"
)

// Deprecated: package memory is a compatibility shim over runtime/discovery.
// New code should import runtime/discovery directly.
type DiscoveryMode = runtimediscovery.Mode

func WithDiscoveryMode(ctx context.Context, mode DiscoveryMode) context.Context {
	return runtimediscovery.WithMode(ctx, mode)
}

func DiscoveryModeFromContext(ctx context.Context) (DiscoveryMode, bool) {
	return runtimediscovery.ModeFromContext(ctx)
}

func MergeDiscoveryMode(ctx context.Context, update DiscoveryMode) context.Context {
	return runtimediscovery.MergeMode(ctx, update)
}
