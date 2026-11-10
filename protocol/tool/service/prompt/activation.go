package prompt

import (
	"context"
	"fmt"
	"github.com/viant/agently-core/protocol/intent"
)

type toolActivationKey struct{}
type knowledgeActivationKey struct{}

// WithToolActivation connects admitted playbooks to the owning turn's existing
// tool resolver. Preparation occurs before message injection; exposure changes
// only after injection succeeds. Inspection does not call this callback.
func WithToolActivation(ctx context.Context, prepare func(context.Context, *intent.Profile) (func() error, error), knowledge ...func(context.Context, *intent.Profile) ([]intent.Message, error)) context.Context {
	ctx = context.WithValue(ctx, toolActivationKey{}, prepare)
	if len(knowledge) > 0 && knowledge[0] != nil {
		ctx = context.WithValue(ctx, knowledgeActivationKey{}, knowledge[0])
	}
	return ctx
}

func prepareKnowledgeActivation(ctx context.Context, profile *intent.Profile) ([]intent.Message, error) {
	if profile == nil || len(profile.Knowledge) == 0 {
		return nil, nil
	}
	prepare, _ := ctx.Value(knowledgeActivationKey{}).(func(context.Context, *intent.Profile) ([]intent.Message, error))
	if prepare == nil {
		return nil, fmt.Errorf("playbook knowledge activation is unavailable")
	}
	return prepare(ctx, profile)
}

func prepareToolActivation(ctx context.Context, profile *intent.Profile) (func() error, error) {
	prepare, _ := ctx.Value(toolActivationKey{}).(func(context.Context, *intent.Profile) (func() error, error))
	if prepare == nil {
		return nil, nil
	}
	return prepare(ctx, profile)
}
