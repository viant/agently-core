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

// Lifecycle customizes role Input.Investigations.
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

func (hooks *Lifecycle) Init(ctx context.Context, entity *Investigation, state xhandler.LifecycleContext[Investigation, xhandler.NoParent, Output]) error {
	if entity == nil {
		return nil
	}
	if hooks.Input == nil || !hooks.Input.Trusted {
		return fmt.Errorf("trusted investigation access is required")
	}
	if strings.TrimSpace(entity.Id) == "" {
		return fmt.Errorf("investigation id is required")
	}
	if hooks.Input.OrphanDelete {
		return invariant.ValidateOrphanDelete(entity, state.Previous, "Id")
	}
	if entity.ShouldDelete {
		if strings.TrimSpace(hooks.Input.ExpectedConversationID) == "" {
			return fmt.Errorf("investigation deletion requires expected conversation")
		}
		return nil
	}
	if state.Previous == nil && entity.Created == nil {
		now := time.Now().UTC()
		entity.SetCreated(&now)
	}
	return nil
}
func (hooks *Lifecycle) Validate(ctx context.Context, entity *Investigation, state xhandler.LifecycleContext[Investigation, xhandler.NoParent, Output]) error {
	return nil
}
func (hooks *Lifecycle) AfterSequence(ctx context.Context, entity *Investigation, state xhandler.LifecycleContext[Investigation, xhandler.NoParent, Output]) error {
	return nil
}
func (hooks *Lifecycle) AfterQueue(ctx context.Context, entity *Investigation, state xhandler.LifecycleContext[Investigation, xhandler.NoParent, Output]) error {
	return nil
}
func (hooks *Lifecycle) Finalize(ctx context.Context, input *Input, output *Output, outcome xhandler.Outcome) error {
	return nil
}
