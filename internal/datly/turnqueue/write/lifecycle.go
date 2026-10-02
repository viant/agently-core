package write

import (
	context "context"
	xhandler "github.com/viant/xdatly/handler"
	reflect "reflect"
	"time"
)

// Lifecycle customizes role Input.Queues.
type Lifecycle struct{}

func LifecycleDatlyType() reflect.Type {
	return reflect.TypeOf((*Lifecycle)(nil)).Elem()
}

var (
	LifecycleHooks = new(Lifecycle)
	LifecycleDatly = LifecycleDatlyType()
)

func (hooks *Lifecycle) Init(ctx context.Context, entity *TurnQueue, state xhandler.LifecycleContext[TurnQueue, xhandler.NoParent, Output]) error {
	if entity == nil || entity.ShouldDelete {
		return nil
	}
	if entity.Has == nil {
		entity.Has = &TurnQueueHas{}
	}
	now := time.Now().UTC()
	if !entity.Has.Status || entity.Status == "" {
		entity.SetStatus("queued")
	}
	if entity.CreatedAt == nil {
		entity.SetCreatedAt(&now)
	}
	if entity.UpdatedAt == nil {
		entity.SetUpdatedAt(&now)
	}
	return nil
}
func (hooks *Lifecycle) Validate(ctx context.Context, entity *TurnQueue, state xhandler.LifecycleContext[TurnQueue, xhandler.NoParent, Output]) error {
	return nil
}
func (hooks *Lifecycle) AfterSequence(ctx context.Context, entity *TurnQueue, state xhandler.LifecycleContext[TurnQueue, xhandler.NoParent, Output]) error {
	return nil
}
func (hooks *Lifecycle) AfterQueue(ctx context.Context, entity *TurnQueue, state xhandler.LifecycleContext[TurnQueue, xhandler.NoParent, Output]) error {
	return nil
}
func (hooks *Lifecycle) Finalize(ctx context.Context, input *Input, output *Output, outcome xhandler.Outcome) error {
	return nil
}
