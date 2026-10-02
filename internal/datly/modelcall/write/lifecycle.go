package write

import (
	context "context"

	"github.com/viant/agently-core/internal/datly/invariant"
	xhandler "github.com/viant/xdatly/handler"
	reflect "reflect"
)

// Lifecycle customizes role Input.ModelCalls.
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

func (hooks *Lifecycle) Init(ctx context.Context, entity *ModelCall, state xhandler.LifecycleContext[ModelCall, xhandler.NoParent, Output]) error {
	if hooks.Input != nil && hooks.Input.OrphanDetach {

		return invariant.ValidateOrphanDetach(entity, state.Previous, hooks.Input.OrphanColumn, []string{"turn_id", "request_payload_id", "response_payload_id", "provider_request_payload_id", "provider_response_payload_id", "stream_payload_id", "run_id"}, "MessageId")
	}

	return nil
}
func (hooks *Lifecycle) Validate(ctx context.Context, entity *ModelCall, state xhandler.LifecycleContext[ModelCall, xhandler.NoParent, Output]) error {
	return nil
}
func (hooks *Lifecycle) AfterSequence(ctx context.Context, entity *ModelCall, state xhandler.LifecycleContext[ModelCall, xhandler.NoParent, Output]) error {
	return nil
}
func (hooks *Lifecycle) AfterQueue(ctx context.Context, entity *ModelCall, state xhandler.LifecycleContext[ModelCall, xhandler.NoParent, Output]) error {
	return nil
}
func (hooks *Lifecycle) Finalize(ctx context.Context, input *Input, output *Output, outcome xhandler.Outcome) error {
	return nil
}

// Legacy deletion acknowledges blank message identities without touching data.
func (input *Input) Init(context.Context) error {
	if input.ModelCalls == nil {
		return nil
	}
	rows := make([]*ModelCall, 0, len(input.ModelCalls))
	for _, row := range input.ModelCalls {
		if row != nil && row.ShouldDelete && row.MessageId == "" {
			continue
		}
		rows = append(rows, row)
	}
	input.ModelCalls = rows
	return nil
}
