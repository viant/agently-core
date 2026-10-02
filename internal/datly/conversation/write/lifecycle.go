package write

import (
	context "context"

	"github.com/viant/agently-core/internal/datly/invariant"
	xhandler "github.com/viant/xdatly/handler"
	reflect "reflect"
	"time"
)

// Lifecycle customizes role Input.Conversations.
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

func (hooks *Lifecycle) Init(ctx context.Context, entity *MutableConversationView, state xhandler.LifecycleContext[MutableConversationView, xhandler.NoParent, Output]) error {
	if hooks.Input != nil && hooks.Input.OrphanDetach {

		return invariant.ValidateOrphanDetach(entity, state.Previous, hooks.Input.OrphanColumn, []string{"conversation_parent_id", "conversation_parent_turn_id", "schedule_id", "schedule_run_id"}, "Id")
	}

	if entity == nil {
		return nil
	}
	now := time.Now()
	if state.Previous == nil {
		entity.SetCreatedAt(&now)
		if entity.Visibility == nil {
			visibility := "private"
			entity.SetVisibility(&visibility)
		}
	}
	entity.SetLastActivity(&now)
	return nil
}
func (hooks *Lifecycle) Validate(ctx context.Context, entity *MutableConversationView, state xhandler.LifecycleContext[MutableConversationView, xhandler.NoParent, Output]) error {
	return nil
}
func (hooks *Lifecycle) AfterSequence(ctx context.Context, entity *MutableConversationView, state xhandler.LifecycleContext[MutableConversationView, xhandler.NoParent, Output]) error {
	return nil
}
func (hooks *Lifecycle) AfterQueue(ctx context.Context, entity *MutableConversationView, state xhandler.LifecycleContext[MutableConversationView, xhandler.NoParent, Output]) error {
	return nil
}
func (hooks *Lifecycle) Finalize(ctx context.Context, input *Input, output *Output, outcome xhandler.Outcome) error {
	return nil
}
