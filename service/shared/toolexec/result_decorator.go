package toolexec

import (
	"context"
	"strings"

	authctx "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/protocol/mcpname"
	requestctx "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/service/reporting/forecastbinding"
)

type DurableResultDecorator func(context.Context, StepInfo, string) (string, bool, error)
type durableResultDecoratorKey struct{}

// WithDurableResultDecorator is an explicit startup/turn injection. With no
// decorator, all existing tools and historical result behavior are unchanged.
func WithDurableResultDecorator(ctx context.Context, fn DurableResultDecorator) context.Context {
	if fn == nil {
		return ctx
	}
	return context.WithValue(ctx, durableResultDecoratorKey{}, fn)
}
func decorateDurableResult(ctx context.Context, step StepInfo, body string) (string, bool, error) {
	fn, _ := ctx.Value(durableResultDecoratorKey{}).(DurableResultDecorator)
	if fn == nil {
		return body, false, nil
	}
	return fn(ctx, step, body)
}

// WithForecastEvidencePlan binds receipt production to a server-admitted plan.
// It does not install report writer/stream enforcement or accept client claims.
func WithForecastEvidencePlan(ctx context.Context, runtime *forecastbinding.Runtime, planID string) context.Context {
	if runtime == nil || planID == "" {
		return ctx
	}
	return WithDurableResultDecorator(ctx, func(ctx context.Context, step StepInfo, _ string) (string, bool, error) {
		if !strings.EqualFold(mcpname.Display(step.Name), "steward/ForecastingCube") {
			return "", false, nil
		}
		turn, ok := requestctx.TurnMetaFromContext(ctx)
		if !ok {
			return "", true, forecastDecoratorError("missing turn")
		}
		body, e := runtime.DecorateAndPublishCompleted(ctx, forecastbinding.Scope{OwnerID: authctx.EffectiveUserID(ctx), ConversationID: turn.ConversationID, TurnID: turn.TurnID}, planID, step.ID)
		return string(body), true, e
	})
}

type forecastDecoratorError string

func (e forecastDecoratorError) Error() string { return "forecast receipt: " + string(e) }
