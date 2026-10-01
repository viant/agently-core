package write

import (
	context "context"
	"errors"
	"fmt"
	xhandler "github.com/viant/xdatly/handler"
	reflect "reflect"
	"strings"
	"time"
)

var (
	ErrAlreadyExists = errors.New("shared artifact already exists")
	ErrNotFound      = errors.New("shared artifact not found")
	ErrOwnerDenied   = errors.New("shared artifact owner denied")
)

// Lifecycle customizes role Input.Artifact.
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

func (hooks *Lifecycle) Init(ctx context.Context, entity *SharedArtifact, state xhandler.LifecycleContext[SharedArtifact, xhandler.NoParent, Output]) error {
	if entity == nil {
		return nil
	}
	if hooks.Input == nil {
		return fmt.Errorf("artifact request context is unavailable")
	}
	if strings.TrimSpace(entity.ArtifactId) == "" {
		return fmt.Errorf("shared artifact artifactId is required")
	}
	if strings.TrimSpace(entity.OwnerId) == "" {
		return fmt.Errorf("shared artifact ownerId is required")
	}
	if !hooks.Input.Internal {
		if hooks.Input.OwnerSubject == nil || strings.TrimSpace(*hooks.Input.OwnerSubject) == "" || entity.OwnerId != *hooks.Input.OwnerSubject {
			return ErrOwnerDenied
		}
		if state.Previous != nil && state.Previous.OwnerId != *hooks.Input.OwnerSubject {
			return ErrOwnerDenied
		}
	}
	switch hooks.Input.WriteIntent {
	case "", "upsert":
	case "create":
		if entity.ShouldDelete {
			return fmt.Errorf("create intent cannot delete a shared artifact")
		}
		if state.Previous != nil {
			return ErrAlreadyExists
		}
	case "update":
		if entity.ShouldDelete {
			return fmt.Errorf("update intent cannot delete a shared artifact")
		}
		if state.Previous == nil {
			return ErrNotFound
		}
	default:
		return fmt.Errorf("unsupported shared artifact write intent %q", hooks.Input.WriteIntent)
	}
	if entity.ShouldDelete && state.Previous == nil {
		return ErrNotFound
	}
	if hooks.now == nil {
		now := time.Now().UTC()
		hooks.now = &now
	}
	if entity.ShouldDelete {
		return nil
	}
	if state.Previous == nil {
		if entity.Has == nil {
			entity.Has = &SharedArtifactHas{}
		}
		if !entity.Has.OwnerRef {
			entity.SetOwnerRef("")
		}
		if !entity.Has.ReportId {
			entity.SetReportId("")
		}
		if !entity.Has.Title {
			entity.SetTitle("")
		}
		if !entity.Has.SourceArtifactId {
			entity.SetSourceArtifactId("")
		}
		if !entity.Has.BaseArtifactRef {
			entity.SetBaseArtifactRef("")
		}
		if !entity.Has.PolicyRef {
			entity.SetPolicyRef("")
		}
	}
	if entity.CreatedAt.IsZero() {
		entity.SetCreatedAt(*hooks.now)
	}
	if entity.UpdatedAt == nil {
		entity.SetUpdatedAt(hooks.now)
	}
	return nil
}
func (hooks *Lifecycle) Validate(ctx context.Context, entity *SharedArtifact, state xhandler.LifecycleContext[SharedArtifact, xhandler.NoParent, Output]) error {
	return nil
}
func (hooks *Lifecycle) AfterSequence(ctx context.Context, entity *SharedArtifact, state xhandler.LifecycleContext[SharedArtifact, xhandler.NoParent, Output]) error {
	return nil
}
func (hooks *Lifecycle) AfterQueue(ctx context.Context, entity *SharedArtifact, state xhandler.LifecycleContext[SharedArtifact, xhandler.NoParent, Output]) error {
	return nil
}
func (hooks *Lifecycle) Finalize(ctx context.Context, input *Input, output *Output, outcome xhandler.Outcome) error {
	if output == nil || output.Data == nil {
		return nil
	}
	copy := *output.Data
	copy.ReportDocumentJson = cloneArtifactBytes(copy.ReportDocumentJson)
	copy.ReportSpecJson = cloneArtifactBytes(copy.ReportSpecJson)
	copy.CompileStateJson = cloneArtifactBytes(copy.CompileStateJson)
	copy.ReportFillJson = cloneArtifactBytes(copy.ReportFillJson)
	copy.ReportPrintJson = cloneArtifactBytes(copy.ReportPrintJson)
	copy.SavedViewOverlayJson = cloneArtifactBytes(copy.SavedViewOverlayJson)
	copy.MetadataJson = cloneArtifactBytes(copy.MetadataJson)
	copy.Has = nil
	output.Data = &copy
	return nil
}

func cloneArtifactBytes(value []byte) []byte {
	if len(value) == 0 {
		return value
	}
	return append([]byte(nil), value...)
}

func (input *Input) Init(context.Context) error {
	if input.Artifact == nil {
		return fmt.Errorf("shared artifact is required")
	}
	if !input.Internal && (input.OwnerSubject == nil || strings.TrimSpace(*input.OwnerSubject) == "") {
		return fmt.Errorf("shared artifact caller is required")
	}
	return nil
}
