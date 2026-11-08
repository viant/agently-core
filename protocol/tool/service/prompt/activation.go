package prompt

import (
	"context"
	"github.com/viant/agently-core/protocol/intent"
)

type toolActivationKey struct{}

// WithToolActivation connects admitted playbooks to the owning turn's existing
// tool resolver. Preparation occurs before message injection; exposure changes
// only after injection succeeds. Inspection does not call this callback.
func WithToolActivation(ctx context.Context, prepare func(context.Context, *intent.Profile) (func() error, error)) context.Context {
	return context.WithValue(ctx, toolActivationKey{}, prepare)
}

func prepareToolActivation(ctx context.Context, profile *intent.Profile) (func() error, error) {
	prepare, _ := ctx.Value(toolActivationKey{}).(func(context.Context, *intent.Profile) (func() error, error))
	if prepare == nil {
		return nil, nil
	}
	return prepare(ctx, profile)
}
