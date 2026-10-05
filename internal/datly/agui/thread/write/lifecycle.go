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

func (h *Lifecycle) Init(_ context.Context, row *Thread, state xhandler.LifecycleContext[Thread, xhandler.NoParent, Output]) error {
	if row == nil || h.Input == nil || (row.Principal == nil || *row.Principal != h.Input.Principal) || row.Id == nil || *row.Id == "" {
		return store.ErrNotFound
	}
	prev := state.Previous
	switch h.Input.Mode {
	case "create":
		if prev != nil || row.Revision != 1 || row.CreatedByUserId == nil || *row.CreatedByUserId != h.Input.Principal || (row.ThreadId == nil || *row.ThreadId == "") || row.ThreadKey == nil || *row.ThreadKey == "" {
			return store.ErrConflict
		}
	case "bind":
		if prev == nil || prev.CreatedByUserId == nil || *prev.CreatedByUserId != h.Input.Principal || prev.Revision != 0 || (prev.Principal != nil && *prev.Principal != "") || (prev.ThreadKey != nil && *prev.ThreadKey != "") || row.Revision != 0 || (row.ThreadId == nil || *row.ThreadId == "") || row.ThreadKey == nil || *row.ThreadKey == "" {
			return store.ErrConflict
		}
		row.SetRevision(1)
	case "update":
		if prev == nil || (prev.Principal == nil || *prev.Principal != h.Input.Principal) {
			return store.ErrNotFound
		}
		if h.Input.Has == nil || !h.Input.Has.DesiredRevision || h.Input.DesiredRevision != row.Revision+1 {
			return store.ErrConflict
		}
		if row.Has != nil {
			if row.Has.ThreadId && (row.ThreadId == nil || prev.ThreadId == nil || *row.ThreadId != *prev.ThreadId) {
				return store.ErrConflict
			}
			if row.Has.ThreadKey && (row.ThreadKey == nil || prev.ThreadKey == nil || *row.ThreadKey != *prev.ThreadKey) {
				return store.ErrConflict
			}
			if row.Has.CreatedByUserId && (row.CreatedByUserId == nil || prev.CreatedByUserId == nil || *row.CreatedByUserId != *prev.CreatedByUserId) {
				return store.ErrConflict
			}
			if row.Has.ProtocolOnly && row.ProtocolOnly && !prev.ProtocolOnly {
				return store.ErrConflict
			}
		}
		row.SetRevision(h.Input.DesiredRevision)
	default:
		return store.ErrInvalidTransition
	}
	return nil
}
func (*Lifecycle) Validate(context.Context, *Thread, xhandler.LifecycleContext[Thread, xhandler.NoParent, Output]) error {
	return nil
}
func (*Lifecycle) AfterSequence(context.Context, *Thread, xhandler.LifecycleContext[Thread, xhandler.NoParent, Output]) error {
	return nil
}
func (*Lifecycle) AfterQueue(context.Context, *Thread, xhandler.LifecycleContext[Thread, xhandler.NoParent, Output]) error {
	return nil
}
func (*Lifecycle) Finalize(context.Context, *Input, *Output, xhandler.Outcome) error { return nil }
