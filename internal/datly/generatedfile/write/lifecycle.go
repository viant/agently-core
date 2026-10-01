package write

import (
	context "context"

	"github.com/viant/agently-core/internal/datly/invariant"
	xhandler "github.com/viant/xdatly/handler"
	reflect "reflect"
	"time"
)

// Lifecycle customizes role Input.GeneratedFiles.
type Lifecycle struct {
	Input *Input `bind:"kind=input"`
}

func LifecycleDatlyType() reflect.Type {
	return reflect.TypeOf((*Lifecycle)(nil)).Elem()
}

var (
	LifecycleHooks = new(Lifecycle)
	LifecycleDatly = LifecycleDatlyType()
)

func (hooks *Lifecycle) Init(ctx context.Context, entity *GeneratedFile, state xhandler.LifecycleContext[GeneratedFile, xhandler.NoParent, Output]) error {
	if hooks.Input != nil && hooks.Input.OrphanDetach {

		return invariant.ValidateOrphanDetach(entity, state.Previous, hooks.Input.OrphanColumn, []string{"turn_id", "message_id", "payload_id"}, "Id")
	}

	if entity == nil || entity.ShouldDelete {
		return nil
	}
	if entity.Has == nil {
		entity.Has = &GeneratedFileHas{}
	}
	now := time.Now()
	if state.Previous == nil {
		if !entity.Has.Status || entity.Status == "" {
			entity.SetStatus("ready")
		}
		if !entity.Has.CreatedAt {
			entity.SetCreatedAt(&now)
		}
	}
	if !entity.Has.UpdatedAt {
		entity.SetUpdatedAt(&now)
	}
	return nil
}
func (hooks *Lifecycle) Validate(ctx context.Context, entity *GeneratedFile, state xhandler.LifecycleContext[GeneratedFile, xhandler.NoParent, Output]) error {
	return nil
}
func (hooks *Lifecycle) AfterSequence(ctx context.Context, entity *GeneratedFile, state xhandler.LifecycleContext[GeneratedFile, xhandler.NoParent, Output]) error {
	return nil
}
func (hooks *Lifecycle) AfterQueue(ctx context.Context, entity *GeneratedFile, state xhandler.LifecycleContext[GeneratedFile, xhandler.NoParent, Output]) error {
	return nil
}
func (hooks *Lifecycle) Finalize(ctx context.Context, input *Input, output *Output, outcome xhandler.Outcome) error {
	return nil
}
