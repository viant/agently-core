package toolexec

import (
	"context"

	asynccfg "github.com/viant/agently-core/protocol/async"
	asyncnarrator "github.com/viant/agently-core/protocol/async/narrator"
)

type asyncNarratorRunnerKey struct{}

// WithAsyncManager attaches an async Manager to ctx. Thin wrapper over
// async.WithManager — kept for backwards compatibility with existing
// call sites in the service layer. Prefer the canonical
// async.WithManager directly in new code.
func WithAsyncManager(ctx context.Context, manager *asynccfg.Manager) context.Context {
	return asynccfg.WithManager(ctx, manager)
}

// AsyncManagerFromContext returns the concrete async Manager attached
// to ctx. Returns (nil, false) if none is present.
//
// Formerly returned an interface that mirrored a subset of *Manager's
// methods. The interface was unused as an abstraction — there was only
// one concrete implementation and no alternative backend — so callers
// now depend on the concrete type directly. Adding a new method to the
// Manager is a one-place change; nothing else has to track it.
func AsyncManagerFromContext(ctx context.Context) (*asynccfg.Manager, bool) {
	return asynccfg.ManagerFromContext(ctx)
}

func WithAsyncNarratorRunner(ctx context.Context, runner asyncnarrator.LLMRunner) context.Context {
	if runner == nil {
		return ctx
	}
	return context.WithValue(ctx, asyncNarratorRunnerKey{}, runner)
}

func AsyncNarratorRunnerFromContext(ctx context.Context) (asyncnarrator.LLMRunner, bool) {
	if ctx == nil {
		return nil, false
	}
	runner, ok := ctx.Value(asyncNarratorRunnerKey{}).(asyncnarrator.LLMRunner)
	return runner, ok && runner != nil
}
