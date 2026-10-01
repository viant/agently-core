package write

import (
	context "context"
	xhandler "github.com/viant/xdatly/handler"
	reflect "reflect"
	"time"
)

// Lifecycle customizes role Input.Users.
type Lifecycle struct{}

func LifecycleDatlyType() reflect.Type {
	return reflect.TypeOf((*Lifecycle)(nil)).Elem()
}

var (
	LifecycleHooks = new(Lifecycle)
	LifecycleDatly = LifecycleDatlyType()
)

func (hooks *Lifecycle) Init(ctx context.Context, entity *User, state xhandler.LifecycleContext[User, xhandler.NoParent, Output]) error {
	if entity == nil {
		return nil
	}
	now := time.Now().UTC()
	if state.Previous == nil {
		if entity.CreatedAt == nil {
			entity.SetCreatedAt(&now)
		}
		if entity.Disabled == nil {
			zero := 0
			entity.SetDisabled(&zero)
		}
		return nil
	}
	if entity.UpdatedAt == nil {
		entity.SetUpdatedAt(&now)
	}
	return nil
}
func (hooks *Lifecycle) Validate(ctx context.Context, entity *User, state xhandler.LifecycleContext[User, xhandler.NoParent, Output]) error {
	return nil
}
func (hooks *Lifecycle) AfterSequence(ctx context.Context, entity *User, state xhandler.LifecycleContext[User, xhandler.NoParent, Output]) error {
	return nil
}
func (hooks *Lifecycle) AfterQueue(ctx context.Context, entity *User, state xhandler.LifecycleContext[User, xhandler.NoParent, Output]) error {
	return nil
}
func (hooks *Lifecycle) Finalize(ctx context.Context, input *Input, output *Output, outcome xhandler.Outcome) error {
	return nil
}
