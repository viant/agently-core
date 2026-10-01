package write

import (
	context "context"

	"github.com/viant/agently-core/internal/datly/invariant"
	xhandler "github.com/viant/xdatly/handler"
	reflect "reflect"
	"time"
)

// Lifecycle customizes role Input.Queues.
type Lifecycle struct {
	Input *Input `bind:"kind=input"`
	now   *time.Time
}

func LifecycleDatlyType() reflect.Type {
	return reflect.TypeOf((*Lifecycle)(nil)).Elem()
}

var (
	LifecycleHooks = new(Lifecycle)
	LifecycleDatly = LifecycleDatlyType()
)

func (hooks *Lifecycle) Init(ctx context.Context, entity *ToolApprovalQueue, state xhandler.LifecycleContext[ToolApprovalQueue, xhandler.NoParent, Output]) error {
	if hooks.Input != nil && hooks.Input.OrphanDetach {

		return invariant.ValidateOrphanDetach(entity, state.Previous, hooks.Input.OrphanColumn, []string{"conversation_id", "turn_id", "message_id"}, "Id")
	}

	if entity == nil || entity.ShouldDelete {
		return nil
	}
	if hooks.now == nil {
		now := time.Now().UTC()
		hooks.now = &now
	}
	if entity.Has == nil {
		entity.Has = &ToolApprovalQueueHas{}
	}
	if previous := state.Previous; previous != nil {
		if !entity.Has.UserId {
			entity.UserId = previous.UserId
		}
		if !entity.Has.ToolName {
			entity.ToolName = previous.ToolName
		}
		if !entity.Has.Arguments {
			entity.Arguments = append([]byte(nil), previous.Arguments...)
		}
		if !entity.Has.Status {
			entity.Status = previous.Status
		}
		if entity.CreatedAt == nil {
			entity.CreatedAt = previous.CreatedAt
		}
	} else {
		if !entity.Has.Status || entity.Status == "" {
			entity.SetStatus("pending")
		}
		if entity.CreatedAt == nil {
			entity.SetCreatedAt(hooks.now)
		}
	}
	if entity.UpdatedAt == nil {
		entity.SetUpdatedAt(hooks.now)
	}
	return nil
}
func (hooks *Lifecycle) Validate(ctx context.Context, entity *ToolApprovalQueue, state xhandler.LifecycleContext[ToolApprovalQueue, xhandler.NoParent, Output]) error {
	return nil
}
func (hooks *Lifecycle) AfterSequence(ctx context.Context, entity *ToolApprovalQueue, state xhandler.LifecycleContext[ToolApprovalQueue, xhandler.NoParent, Output]) error {
	return nil
}
func (hooks *Lifecycle) AfterQueue(ctx context.Context, entity *ToolApprovalQueue, state xhandler.LifecycleContext[ToolApprovalQueue, xhandler.NoParent, Output]) error {
	return nil
}
func (hooks *Lifecycle) Finalize(ctx context.Context, input *Input, output *Output, outcome xhandler.Outcome) error {
	return nil
}
