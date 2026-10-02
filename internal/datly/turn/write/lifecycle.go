package write

import (
	context "context"

	"github.com/viant/agently-core/internal/datly/invariant"
	xhandler "github.com/viant/xdatly/handler"
	reflect "reflect"
	"time"
)

// Lifecycle customizes role Input.Turns.
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

func (hooks *Lifecycle) Init(ctx context.Context, entity *Turn, state xhandler.LifecycleContext[Turn, xhandler.NoParent, Output]) error {
	if hooks.Input != nil && hooks.Input.OrphanDetach {

		return invariant.ValidateOrphanDetach(entity, state.Previous, hooks.Input.OrphanColumn, []string{"goal_id", "started_by_message_id", "retry_of", "run_id"}, "Id")
	}

	if entity == nil || entity.ShouldDelete {
		return nil
	}
	if state.Previous == nil {
		now := time.Now()
		entity.SetCreatedAt(&now)
	}
	return nil
}
func (hooks *Lifecycle) Validate(ctx context.Context, entity *Turn, state xhandler.LifecycleContext[Turn, xhandler.NoParent, Output]) error {
	return nil
}
func (hooks *Lifecycle) AfterSequence(ctx context.Context, entity *Turn, state xhandler.LifecycleContext[Turn, xhandler.NoParent, Output]) error {
	return nil
}
func (hooks *Lifecycle) AfterQueue(ctx context.Context, entity *Turn, state xhandler.LifecycleContext[Turn, xhandler.NoParent, Output]) error {
	return nil
}
func (hooks *Lifecycle) Finalize(ctx context.Context, input *Input, output *Output, outcome xhandler.Outcome) error {
	return nil
}

// Legacy turn deletion acknowledges blank identities without touching storage.
func (input *Input) Init(context.Context) error {
	if input.Turns == nil {
		return nil
	}
	rows := make([]*Turn, 0, len(input.Turns))
	for _, row := range input.Turns {
		if row != nil && row.ShouldDelete && row.Id == "" {
			continue
		}
		rows = append(rows, row)
	}
	input.Turns = rows
	return nil
}
