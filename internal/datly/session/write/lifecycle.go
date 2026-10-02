package write

import (
	context "context"
	xhandler "github.com/viant/xdatly/handler"
	reflect "reflect"
	"time"
)

// Lifecycle customizes role Input.Session.
type Lifecycle struct{}

func LifecycleDatlyType() reflect.Type {
	return reflect.TypeOf((*Lifecycle)(nil)).Elem()
}

var (
	LifecycleHooks = new(Lifecycle)
	LifecycleDatly = LifecycleDatlyType()
)

func (hooks *Lifecycle) Init(ctx context.Context, entity *Session, state xhandler.LifecycleContext[Session, xhandler.NoParent, Output]) error {
	if entity == nil || entity.ShouldDelete {
		return nil
	}
	now := time.Now().UTC()
	if state.Previous == nil {
		entity.SetCreatedAt(now)
		return nil
	}
	// Legacy session updates deliberately mark these values, even when omitted.
	entity.SetId(entity.Id)
	entity.SetUserId(entity.UserId)
	entity.SetProvider(entity.Provider)
	entity.SetExpiresAt(entity.ExpiresAt)
	if entity.UpdatedAt == nil {
		entity.SetUpdatedAt(&now)
	}
	return nil
}
func (hooks *Lifecycle) Validate(ctx context.Context, entity *Session, state xhandler.LifecycleContext[Session, xhandler.NoParent, Output]) error {
	return nil
}
func (hooks *Lifecycle) AfterSequence(ctx context.Context, entity *Session, state xhandler.LifecycleContext[Session, xhandler.NoParent, Output]) error {
	return nil
}
func (hooks *Lifecycle) AfterQueue(ctx context.Context, entity *Session, state xhandler.LifecycleContext[Session, xhandler.NoParent, Output]) error {
	return nil
}
func (hooks *Lifecycle) Finalize(ctx context.Context, input *Input, output *Output, outcome xhandler.Outcome) error {
	return nil
}
