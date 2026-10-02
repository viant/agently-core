package write

import (
	context "context"
	"errors"
	"fmt"
	xhandler "github.com/viant/xdatly/handler"
	reflect "reflect"
	"strings"
)

// Lifecycle customizes role Input.Contexts.
type Lifecycle struct {
	Input *Input `bind:"kind=input"`
}

var (
	ErrOwnerDenied = errors.New("report context owner denied")
	ErrNotFound    = errors.New("report context not found")
	ErrCASMismatch = errors.New("report context revision mismatch")
)

func LifecycleDatlyType() reflect.Type {
	return reflect.TypeOf((*Lifecycle)(nil)).Elem()
}

var (
	LifecycleHooks = new(Lifecycle)
	LifecycleDatly = LifecycleDatlyType()
)

func (hooks *Lifecycle) Init(ctx context.Context, entity *Context, state xhandler.LifecycleContext[Context, xhandler.NoParent, Output]) error {
	if entity == nil {
		return nil
	}
	if hooks.Input == nil {
		return fmt.Errorf("report context input is unavailable")
	}
	if !hooks.Input.Internal {
		if hooks.Input.OwnerSubject == nil || strings.TrimSpace(*hooks.Input.OwnerSubject) == "" || strings.TrimSpace(entity.OwnerId) != strings.TrimSpace(*hooks.Input.OwnerSubject) {
			return ErrOwnerDenied
		}
	}
	if entity.ShouldDelete {
		return nil
	}
	if hooks.Input.Has == nil || !hooks.Input.Has.DesiredRevision {
		return fmt.Errorf("desired report context revision is required")
	}
	if state.Previous == nil && entity.Revision != 0 {
		return ErrNotFound
	}
	if state.Previous != nil && entity.Revision == 0 {
		return ErrCASMismatch
	}
	entity.SetRevision(hooks.Input.DesiredRevision)
	return nil
}
func (hooks *Lifecycle) Validate(ctx context.Context, entity *Context, state xhandler.LifecycleContext[Context, xhandler.NoParent, Output]) error {
	return nil
}
func (hooks *Lifecycle) AfterSequence(ctx context.Context, entity *Context, state xhandler.LifecycleContext[Context, xhandler.NoParent, Output]) error {
	return nil
}
func (hooks *Lifecycle) AfterQueue(ctx context.Context, entity *Context, state xhandler.LifecycleContext[Context, xhandler.NoParent, Output]) error {
	return nil
}
func (hooks *Lifecycle) Finalize(ctx context.Context, input *Input, output *Output, outcome xhandler.Outcome) error {
	return nil
}
