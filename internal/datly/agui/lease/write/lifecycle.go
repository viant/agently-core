package write

import (
	"context"
	store "github.com/viant/agently-core/app/store/agui"
	xhandler "github.com/viant/xdatly/handler"
	"reflect"
)

type Lifecycle struct {
	Input *Input `bind:"kind=input"`
}

func LifecycleDatlyType() reflect.Type { return reflect.TypeFor[Lifecycle]() }

var LifecycleDatly = LifecycleDatlyType()

func (h *Lifecycle) Init(_ context.Context, row *Lease, state xhandler.LifecycleContext[Lease, xhandler.NoParent, Output]) error {
	if h.Input == nil || row == nil || row.Id == nil || row.Principal == nil || *row.Principal != h.Input.Principal || (row.Owner == nil || *row.Owner == "") || row.LeaseUntil == nil || row.LeaseUntil.IsZero() {
		return store.ErrConflict
	}
	prev := state.Previous
	if prev == nil || prev.Principal == nil || *prev.Principal != h.Input.Principal || prev.RunKind == nil || *prev.RunKind != "agui" {
		return store.ErrNotFound
	}
	if h.Input.Mode != "update" || h.Input.Has == nil || !h.Input.Has.DesiredRevision || h.Input.DesiredRevision != row.Revision+1 {
		return store.ErrConflict
	}
	if row.RunKind != nil && *row.RunKind != "agui" {
		return store.ErrConflict
	}
	if row.RunKey != nil && (prev.RunKey == nil || *row.RunKey != *prev.RunKey) {
		return store.ErrConflict
	}
	row.SetRevision(h.Input.DesiredRevision)
	return nil
}
func (*Lifecycle) Validate(context.Context, *Lease, xhandler.LifecycleContext[Lease, xhandler.NoParent, Output]) error {
	return nil
}
func (*Lifecycle) AfterSequence(context.Context, *Lease, xhandler.LifecycleContext[Lease, xhandler.NoParent, Output]) error {
	return nil
}
func (*Lifecycle) AfterQueue(context.Context, *Lease, xhandler.LifecycleContext[Lease, xhandler.NoParent, Output]) error {
	return nil
}
func (*Lifecycle) Finalize(context.Context, *Input, *Output, xhandler.Outcome) error { return nil }
