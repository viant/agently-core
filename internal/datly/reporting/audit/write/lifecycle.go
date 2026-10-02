package write

import (
	context "context"
	"fmt"
	"github.com/google/uuid"
	xhandler "github.com/viant/xdatly/handler"
	reflect "reflect"
	"strings"
	"time"
)

// Lifecycle customizes role Input.Events.
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

func (hooks *Lifecycle) Init(ctx context.Context, entity *AuditEvent, state xhandler.LifecycleContext[AuditEvent, xhandler.NoParent, Output]) error {
	if entity == nil {
		return nil
	}
	if hooks.Input == nil || !hooks.Input.Trusted {
		return fmt.Errorf("trusted report-audit access is required")
	}
	if !entity.ShouldDelete && strings.TrimSpace(entity.EventId) == "" {
		entity.SetEventId(uuid.NewString())
	}
	if strings.TrimSpace(entity.EventId) == "" {
		return fmt.Errorf("report-audit event id is required")
	}
	if entity.ShouldDelete {
		if hooks.Input.RetentionDelete {
			if hooks.Input.Has == nil || !hooks.Input.Has.RetentionCutoff || hooks.Input.RetentionCutoff.IsZero() || state.Previous == nil || state.Previous.OccurredAt == nil || state.Previous.OccurredAt.After(hooks.Input.RetentionCutoff) {
				return fmt.Errorf("report-audit retention delete requires an existing row before cutoff")
			}
			return nil
		}
		if strings.TrimSpace(hooks.Input.ExpectedJobID) == "" && strings.TrimSpace(hooks.Input.ExpectedArtifactID) == "" {
			return fmt.Errorf("report-audit deletion requires expected job or artifact")
		}
		return nil
	}
	if state.Previous != nil {
		return fmt.Errorf("report-audit events are append-only")
	}
	if strings.TrimSpace(entity.EventType) == "" || strings.TrimSpace(entity.ArtifactRef) == "" || strings.TrimSpace(entity.ActorId) == "" {
		return fmt.Errorf("report-audit event type, artifact reference and actor are required")
	}
	if entity.OccurredAt == nil {
		now := time.Now().UTC()
		entity.SetOccurredAt(&now)
	}
	return nil
}
func (hooks *Lifecycle) Validate(ctx context.Context, entity *AuditEvent, state xhandler.LifecycleContext[AuditEvent, xhandler.NoParent, Output]) error {
	return nil
}
func (hooks *Lifecycle) AfterSequence(ctx context.Context, entity *AuditEvent, state xhandler.LifecycleContext[AuditEvent, xhandler.NoParent, Output]) error {
	return nil
}
func (hooks *Lifecycle) AfterQueue(ctx context.Context, entity *AuditEvent, state xhandler.LifecycleContext[AuditEvent, xhandler.NoParent, Output]) error {
	return nil
}
func (hooks *Lifecycle) Finalize(ctx context.Context, input *Input, output *Output, outcome xhandler.Outcome) error {
	return nil
}
