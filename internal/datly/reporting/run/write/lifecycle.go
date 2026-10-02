package write

import (
	context "context"
	"errors"
	"fmt"

	"github.com/viant/agently-core/internal/datly/invariant"
	xhandler "github.com/viant/xdatly/handler"
	reflect "reflect"
	"strings"
)

// Lifecycle customizes role Input.Runs.
type Lifecycle struct {
	Input *Input `bind:"kind=input"`
}

var (
	ErrAlreadyExists = errors.New("report run already exists")
	ErrNotFound      = errors.New("report run not found")
	ErrCASMismatch   = errors.New("report run revision mismatch")
	ErrImmutable     = errors.New("completed report run is immutable")
	ErrOwnerDenied   = errors.New("report run owner denied")
)

func LifecycleDatlyType() reflect.Type {
	return reflect.TypeOf((*Lifecycle)(nil)).Elem()
}

var (
	LifecycleHooks = new(Lifecycle)
	LifecycleDatly = LifecycleDatlyType()
)

func (hooks *Lifecycle) Init(ctx context.Context, entity *Run, state xhandler.LifecycleContext[Run, xhandler.NoParent, Output]) error {
	if entity == nil {
		return nil
	}
	if hooks.Input != nil && hooks.Input.OrphanDetach {
		if hooks.Input.Mode != "orphanDetach" {
			return fmt.Errorf("orphan detach requires dedicated mode")
		}
		return invariant.ValidateOrphanDetach(entity, state.Previous, hooks.Input.OrphanColumn, []string{"conversation_id"}, "ReportRunId", "Revision")
	}
	if hooks.Input == nil {
		return fmt.Errorf("report run input is unavailable")
	}
	if strings.TrimSpace(entity.ReportRunId) == "" || strings.TrimSpace(entity.OwnerId) == "" {
		return ErrNotFound
	}
	if !hooks.Input.Internal && (hooks.Input.OwnerSubject == nil || strings.TrimSpace(*hooks.Input.OwnerSubject) != strings.TrimSpace(entity.OwnerId)) {
		return ErrOwnerDenied
	}
	switch hooks.Input.Mode {
	case "create":
		if entity.ShouldDelete {
			return fmt.Errorf("create cannot delete a report run")
		}
		if state.Previous != nil {
			return ErrAlreadyExists
		}
		if !validRunStatus(entity.Status) || strings.TrimSpace(entity.Materializer) == "" || strings.TrimSpace(entity.UiRunRequestId) == "" {
			return fmt.Errorf("report run requires materializer, status and request ID")
		}
		return nil
	case "update", "adopt":
		if entity.ShouldDelete {
			return fmt.Errorf("%s cannot delete a report run", hooks.Input.Mode)
		}
		previous := state.Previous
		if previous == nil {
			return ErrNotFound
		}
		if hooks.Input.Has == nil || !hooks.Input.Has.DesiredRevision || hooks.Input.DesiredRevision != entity.Revision+1 {
			return ErrCASMismatch
		}
		if hooks.Input.Has == nil || !hooks.Input.Has.ExpectedRequestID || strings.TrimSpace(hooks.Input.ExpectedRequestID) != strings.TrimSpace(previous.UiRunRequestId) ||
			strings.TrimSpace(entity.UiRunRequestId) != strings.TrimSpace(previous.UiRunRequestId) || entity.OwnerId != previous.OwnerId {
			return ErrNotFound
		}
		if hooks.Input.Mode == "update" {
			if previous.Status == "completed" {
				return ErrImmutable
			}
			if !validRunStatus(entity.Status) {
				return fmt.Errorf("invalid report run status %q", entity.Status)
			}
		} else if err := validateAdoption(previous, entity); err != nil {
			return err
		}
		entity.SetRevision(hooks.Input.DesiredRevision)
		return nil
	case "delete":
		if !entity.ShouldDelete {
			return fmt.Errorf("delete mode requires a report run delete marker")
		}
		return nil
	default:
		return fmt.Errorf("unsupported report run mutation mode %q", hooks.Input.Mode)
	}
}

func validRunStatus(status string) bool {
	switch strings.TrimSpace(status) {
	case "running", "completed", "failed":
		return true
	}
	return false
}

func runText(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func validateAdoption(previous, next *Run) error {
	if previous.Status != "completed" || next.Status != "completed" || runText(previous.Origin) != "manual" || runText(previous.ConversationId) != "" {
		return ErrImmutable
	}
	if runText(next.ConversationId) == "" || runText(next.AdoptionSource) == "" || runText(next.ActorId) != strings.TrimSpace(next.OwnerId) {
		return ErrImmutable
	}
	before, after := *previous, *next
	before.Has, after.Has = nil, nil
	after.ConversationId, after.AdoptionSource, after.ActorId = before.ConversationId, before.AdoptionSource, before.ActorId
	after.UpdatedAt, after.Revision = before.UpdatedAt, before.Revision
	if !reflect.DeepEqual(before, after) {
		return ErrImmutable
	}
	return nil
}
func (hooks *Lifecycle) Validate(ctx context.Context, entity *Run, state xhandler.LifecycleContext[Run, xhandler.NoParent, Output]) error {
	return nil
}
func (hooks *Lifecycle) AfterSequence(ctx context.Context, entity *Run, state xhandler.LifecycleContext[Run, xhandler.NoParent, Output]) error {
	return nil
}
func (hooks *Lifecycle) AfterQueue(ctx context.Context, entity *Run, state xhandler.LifecycleContext[Run, xhandler.NoParent, Output]) error {
	return nil
}
func (hooks *Lifecycle) Finalize(ctx context.Context, input *Input, output *Output, outcome xhandler.Outcome) error {
	return nil
}
