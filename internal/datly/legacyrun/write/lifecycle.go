package write

import (
	context "context"
	"fmt"

	"github.com/viant/agently-core/internal/datly/invariant"
	xhandler "github.com/viant/xdatly/handler"
	reflect "reflect"
	"strings"
	"time"
)

// Lifecycle customizes role Input.Runs.
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

func (hooks *Lifecycle) Init(ctx context.Context, entity *LegacyRun, state xhandler.LifecycleContext[LegacyRun, xhandler.NoParent, Output]) error {
	if hooks.Input != nil && hooks.Input.OrphanDetach {
		if !hooks.Input.Trusted || hooks.Input.OrphanDelete {
			return fmt.Errorf("trusted exclusive orphan detach is required")
		}
		return invariant.ValidateOrphanDetach(entity, state.Previous, hooks.Input.OrphanColumn, []string{"conversation_id"}, "Id")
	}

	if entity == nil {
		return nil
	}
	if hooks.Input == nil || !hooks.Input.Trusted {
		return fmt.Errorf("trusted schedule-run access is required")
	}
	if strings.TrimSpace(entity.Id) == "" {
		return fmt.Errorf("schedule run id is required")
	}
	if hooks.Input.OrphanDelete {
		if hooks.Input.OrphanDetach {
			return fmt.Errorf("maintenance modes are mutually exclusive")
		}
		return invariant.ValidateOrphanDelete(entity, state.Previous, "Id")
	}
	if entity.ShouldDelete {
		if strings.TrimSpace(hooks.Input.ExpectedScheduleID) == "" {
			return fmt.Errorf("schedule-run deletion requires expected schedule")
		}
		return nil
	}
	if state.Previous == nil {
		if strings.TrimSpace(entity.ScheduleId) == "" {
			return fmt.Errorf("schedule id is required")
		}
		now := time.Now().UTC()
		if entity.CreatedAt == nil {
			entity.SetCreatedAt(&now)
		}
		if entity.Status == "" {
			entity.SetStatus("pending")
		}
		if entity.ConversationKind == "" {
			entity.SetConversationKind("scheduled")
		}
	}
	return nil
}
func (hooks *Lifecycle) Validate(ctx context.Context, entity *LegacyRun, state xhandler.LifecycleContext[LegacyRun, xhandler.NoParent, Output]) error {
	return nil
}
func (hooks *Lifecycle) AfterSequence(ctx context.Context, entity *LegacyRun, state xhandler.LifecycleContext[LegacyRun, xhandler.NoParent, Output]) error {
	return nil
}
func (hooks *Lifecycle) AfterQueue(ctx context.Context, entity *LegacyRun, state xhandler.LifecycleContext[LegacyRun, xhandler.NoParent, Output]) error {
	return nil
}
func (hooks *Lifecycle) Finalize(ctx context.Context, input *Input, output *Output, outcome xhandler.Outcome) error {
	return nil
}
