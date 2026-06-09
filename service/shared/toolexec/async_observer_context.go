package toolexec

import (
	"context"

	asynccfg "github.com/viant/agently-core/protocol/async"
)

type asyncCompletionObserverKey struct{}

type AsyncCompletionObserver func(ctx context.Context, rec *asynccfg.OperationRecord)

func WithAsyncCompletionObserver(ctx context.Context, observer AsyncCompletionObserver) context.Context {
	if observer == nil {
		return ctx
	}
	return context.WithValue(ctx, asyncCompletionObserverKey{}, observer)
}

func AsyncCompletionObserverFromContext(ctx context.Context) (AsyncCompletionObserver, bool) {
	observer, ok := ctx.Value(asyncCompletionObserverKey{}).(AsyncCompletionObserver)
	return observer, ok && observer != nil
}
