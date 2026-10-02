package write

import (
	context "context"
	"errors"
	"fmt"
	jobread "github.com/viant/agently-core/internal/datly/reporting/job/read"

	"github.com/viant/agently-core/internal/datly/invariant"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/spec"
	xhandler "github.com/viant/xdatly/handler"
	reflect "reflect"
	"strings"
)

// Lifecycle customizes role Input.Artifacts.
type Lifecycle struct {
	Input   *Input                      `bind:"kind=input"`
	Invoker dexec.ComponentInvoker      `bind:"kind=component_invoker"`
	Starter xhandler.TransactionStarter `bind:"kind=transactionStarter"`
}

var (
	ErrAlreadyExists = errors.New("report export artifact already exists")
	ErrNotFound      = errors.New("report export artifact or parent job not found")
	ErrConflict      = errors.New("report export artifact does not match parent job")
	ErrOwnerDenied   = errors.New("report export artifact owner denied")
)

func LifecycleDatlyType() reflect.Type {
	return reflect.TypeOf((*Lifecycle)(nil)).Elem()
}

var (
	LifecycleHooks = new(Lifecycle)
	LifecycleDatly = LifecycleDatlyType()
)

func (hooks *Lifecycle) Init(ctx context.Context, entity *Artifact, state xhandler.LifecycleContext[Artifact, xhandler.NoParent, Output]) error {
	if entity == nil {
		return nil
	}
	if hooks.Input == nil {
		return fmt.Errorf("report export artifact input is unavailable")
	}
	if hooks.Input.OrphanDelete {
		if !hooks.Input.Internal || hooks.Input.Mode != "orphanDelete" {
			return fmt.Errorf("trusted orphan artifact delete requires dedicated mode")
		}
		return invariant.ValidateOrphanDelete(entity, state.Previous, "ArtifactId")
	}
	if strings.TrimSpace(entity.ArtifactId) == "" {
		return ErrNotFound
	}
	if state.Previous != nil && entity.Has != nil && !entity.Has.OwnerId {
		entity.SetOwnerId(state.Previous.OwnerId)
	}
	if strings.TrimSpace(entity.OwnerId) == "" {
		return ErrOwnerDenied
	}
	if !hooks.Input.Internal && (hooks.Input.OwnerSubject == nil || strings.TrimSpace(*hooks.Input.OwnerSubject) != strings.TrimSpace(entity.OwnerId)) {
		return ErrOwnerDenied
	}
	if state.Previous != nil && state.Previous.OwnerId != entity.OwnerId {
		return ErrOwnerDenied
	}
	switch hooks.Input.Mode {
	case "create":
		if entity.ShouldDelete {
			return fmt.Errorf("create cannot delete an artifact")
		}
		if state.Previous != nil {
			return ErrAlreadyExists
		}
		if strings.TrimSpace(entity.JobId) == "" || strings.TrimSpace(entity.ArtifactRef) == "" || strings.TrimSpace(entity.Format) == "" {
			return ErrConflict
		}
		if hooks.Starter == nil || hooks.Invoker == nil {
			return fmt.Errorf("typed parent job lookup is unavailable")
		}
		if err := hooks.Starter.Start(ctx); err != nil {
			return err
		}
		query := &jobread.Input{}
		query.SetJobID(entity.JobId)
		value, err := hooks.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: dexec.ComponentTarget{
			Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[jobread.ReaderComponent]().PkgPath(), Name: "reader"},
			Route:     spec.RouteRef{Method: "GET", Path: "/v1/internal/forge/reporting/job"},
		}, Input: query})
		if err != nil {
			return err
		}
		out, ok := value.(*jobread.Output)
		if !ok || out == nil {
			return fmt.Errorf("parent job reader returned %T", value)
		}
		if len(out.Data) == 0 {
			return ErrNotFound
		}
		if len(out.Data) != 1 || out.Data[0] == nil {
			return fmt.Errorf("parent job lookup returned %d rows", len(out.Data))
		}
		job := out.Data[0]
		if job.OwnerId != entity.OwnerId || strings.TrimSpace(job.ArtifactRef) != strings.TrimSpace(entity.ArtifactRef) ||
			!strings.EqualFold(strings.TrimSpace(job.Format), strings.TrimSpace(entity.Format)) {
			return ErrConflict
		}
		if entity.RetentionTtlSec < 0 {
			entity.SetRetentionTtlSec(0)
		}
		return nil
	case "delete":
		if !entity.ShouldDelete || strings.TrimSpace(hooks.Input.ExpectedJobID) == "" {
			return fmt.Errorf("artifact delete requires marker and expected job")
		}
		return nil
	default:
		return fmt.Errorf("unsupported report export artifact mode %q", hooks.Input.Mode)
	}
}
func (hooks *Lifecycle) Validate(ctx context.Context, entity *Artifact, state xhandler.LifecycleContext[Artifact, xhandler.NoParent, Output]) error {
	return nil
}
func (hooks *Lifecycle) AfterSequence(ctx context.Context, entity *Artifact, state xhandler.LifecycleContext[Artifact, xhandler.NoParent, Output]) error {
	return nil
}
func (hooks *Lifecycle) AfterQueue(ctx context.Context, entity *Artifact, state xhandler.LifecycleContext[Artifact, xhandler.NoParent, Output]) error {
	return nil
}
func (hooks *Lifecycle) Finalize(ctx context.Context, input *Input, output *Output, outcome xhandler.Outcome) error {
	if output == nil {
		return nil
	}
	for i, row := range output.Data {
		if row == nil {
			continue
		}
		copy := *row
		copy.InlineData = append([]byte(nil), row.InlineData...)
		copy.Has = nil
		output.Data[i] = &copy
	}
	return nil
}
