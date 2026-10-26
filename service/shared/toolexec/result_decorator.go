package toolexec

import (
	"context"

	"github.com/viant/agently-core/runtime/evidence"
)

type DurableResultDecorator func(context.Context, StepInfo, string) (string, bool, error)
type durableResultDecoratorKey struct{}

// WithDurableResultDecorator is an explicit startup/turn injection. With no
// decorator, all existing tools and historical result behavior are unchanged.
// Business-schema interpretation belongs to the producer; this hook stays opaque.
func WithDurableResultDecorator(ctx context.Context, fn DurableResultDecorator) context.Context {
	if fn == nil {
		return ctx
	}
	return context.WithValue(ctx, durableResultDecoratorKey{}, fn)
}
func decorateDurableResult(ctx context.Context, step StepInfo, body string) (string, bool, error) {
	if hook := evidence.ToolsFromContext(ctx); hook != nil {
		body, handled, err := hook.Completed(ctx, step.Name, step.ID)
		if handled || err != nil {
			return string(body), handled, err
		}
	}
	fn, _ := ctx.Value(durableResultDecoratorKey{}).(DurableResultDecorator)
	if fn == nil {
		return body, false, nil
	}
	return fn(ctx, step, body)
}
