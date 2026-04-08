package memory

import (
	"context"

	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
)

// Deprecated: package memory is a compatibility shim over runtime/requestctx.
// New code should import runtime/requestctx directly.
type RunMeta = runtimerequestctx.RunMeta

func WithRunMeta(ctx context.Context, meta RunMeta) context.Context {
	return runtimerequestctx.WithRunMeta(ctx, meta)
}

func RunMetaFromContext(ctx context.Context) (RunMeta, bool) {
	return runtimerequestctx.RunMetaFromContext(ctx)
}
