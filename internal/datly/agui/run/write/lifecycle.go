package write

import (
	"bytes"
	"context"
	"fmt"
	store "github.com/viant/agently-core/app/store/agui"
	xhandler "github.com/viant/xdatly/handler"
	"reflect"
)

type Lifecycle struct {
	Input *Input `bind:"kind=input"`
}

func LifecycleDatlyType() reflect.Type { return reflect.TypeFor[Lifecycle]() }

var LifecycleDatly = LifecycleDatlyType()

func (h *Lifecycle) Init(ctx context.Context, row *Run, state xhandler.LifecycleContext[Run, xhandler.NoParent, Output]) error {
	if row == nil || h.Input == nil || row.Principal == nil || *row.Principal != h.Input.Principal || row.Id == nil || *row.Id == "" {
		return store.ErrNotFound
	}
	switch h.Input.Mode {
	case "create":
		if row.RunKind == nil || *row.RunKind != "agui" || row.ConversationId == nil || *row.ConversationId == "" {
			return store.ErrConflict
		}
		if state.Previous != nil {
			return store.ErrConflict
		}
		if row.Revision != 1 || row.LastSequence != 0 || row.Status == nil || *row.Status != store.StatusAdmitted {
			return store.ErrInvalidTransition
		}
	case "update":
		prev := state.Previous
		if prev == nil {
			return store.ErrNotFound
		}
		if h.Input.Has == nil || !h.Input.Has.DesiredRevision || h.Input.DesiredRevision != row.Revision+1 {
			return store.ErrConflict
		}
		if prev.RunKind == nil || *prev.RunKind != "agui" || (row.RunKind != nil && *row.RunKind != "agui") {
			return store.ErrNotFound
		}
		for field, pair := range map[string][2]*string{"conversation": {row.ConversationId, prev.ConversationId}, "key": {row.RunKey, prev.RunKey}, "principal": {row.Principal, prev.Principal}, "inputHash": {row.InputHash, prev.InputHash}, "sourceKey": {row.SourceKey, prev.SourceKey}} {
			if pair[0] != nil && (pair[1] == nil || *pair[0] != *pair[1]) {
				return fmt.Errorf("%w: immutable %s", store.ErrConflict, field)
			}
		}
		if row.Has != nil {
			for field, pair := range map[string][2]string{"run": {row.RunId, prev.RunId}, "parent": {row.ParentRunId, prev.ParentRunId}, "prior": {row.PriorRunId, prev.PriorRunId}, "clientMessage": {row.ClientMessageId, prev.ClientMessageId}} {
				supplied := map[string]bool{"run": row.Has.RunId, "parent": row.Has.ParentRunId, "prior": row.Has.PriorRunId, "clientMessage": row.Has.ClientMessageId}[field]
				if supplied && pair[0] != pair[1] {
					return fmt.Errorf("%w: immutable %s", store.ErrConflict, field)
				}
			}
		}

		if row.InitialTurnKey != nil && (prev.InitialTurnKey == nil || *row.InitialTurnKey != *prev.InitialTurnKey) {
			if prev.InitialTurnKey != nil || prev.TurnId != "" || prev.PriorRunId != "" || row.Has == nil || !row.Has.TurnId || row.TurnId == "" || *row.InitialTurnKey != store.InitialTurnKey(h.Input.Principal, *prev.ConversationId, row.TurnId) {
				return store.ErrConflict
			}
		}
		if row.Has != nil && row.Has.InputJson && !bytes.Equal(row.InputJson, prev.InputJson) {
			return store.ErrConflict
		}
		if row.Has != nil && row.Has.LastSequence && row.LastSequence < prev.LastSequence {
			return store.ErrConflict
		}
		if row.Has != nil && row.Has.TurnId && prev.TurnId != "" && row.TurnId != prev.TurnId {
			return store.ErrConflict
		}
		if row.Has != nil && row.Has.ResumedByRunId && prev.ResumedByRunId != "" && row.ResumedByRunId != prev.ResumedByRunId {
			return store.ErrConflict
		}
		row.SetRevision(h.Input.DesiredRevision)
	default:
		return store.ErrInvalidTransition
	}
	return nil
}
func (*Lifecycle) Validate(context.Context, *Run, xhandler.LifecycleContext[Run, xhandler.NoParent, Output]) error {
	return nil
}
func (*Lifecycle) AfterSequence(context.Context, *Run, xhandler.LifecycleContext[Run, xhandler.NoParent, Output]) error {
	return nil
}
func (*Lifecycle) AfterQueue(context.Context, *Run, xhandler.LifecycleContext[Run, xhandler.NoParent, Output]) error {
	return nil
}
func (*Lifecycle) Finalize(context.Context, *Input, *Output, xhandler.Outcome) error { return nil }
