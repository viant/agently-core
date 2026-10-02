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

// Lifecycle customizes role Input.Jobs.
type Lifecycle struct {
	Input *Input `bind:"kind=input"`
}

var (
	ErrAlreadyExists     = errors.New("report export job already exists")
	ErrNotFound          = errors.New("report export job not found")
	ErrInvalidTransition = errors.New("invalid report export job transition")
	ErrConflict          = errors.New("report export request conflict")
	ErrOwnerDenied       = errors.New("report export job owner denied")
)

func LifecycleDatlyType() reflect.Type {
	return reflect.TypeOf((*Lifecycle)(nil)).Elem()
}

var (
	LifecycleHooks = new(Lifecycle)
	LifecycleDatly = LifecycleDatlyType()
)

func (hooks *Lifecycle) Init(ctx context.Context, entity *Job, state xhandler.LifecycleContext[Job, xhandler.NoParent, Output]) error {
	if entity == nil {
		return nil
	}
	if hooks.Input != nil && hooks.Input.OrphanDetach {
		if hooks.Input.Mode != "orphanDetach" {
			return fmt.Errorf("orphan detach requires dedicated mode")
		}
		return invariant.ValidateOrphanDetach(entity, state.Previous, hooks.Input.OrphanColumn, []string{"conversation_id"}, "JobId")
	}
	if hooks.Input == nil {
		return fmt.Errorf("report export job input is unavailable")
	}
	if strings.TrimSpace(entity.JobId) == "" {
		return ErrNotFound
	}
	previous := state.Previous
	if previous != nil && entity.Has != nil && !entity.Has.OwnerId {
		entity.SetOwnerId(previous.OwnerId)
	}
	if strings.TrimSpace(entity.OwnerId) == "" {
		return ErrOwnerDenied
	}
	if !hooks.Input.Internal && (hooks.Input.OwnerSubject == nil || strings.TrimSpace(*hooks.Input.OwnerSubject) != strings.TrimSpace(entity.OwnerId)) {
		return ErrOwnerDenied
	}
	if previous != nil && previous.OwnerId != entity.OwnerId {
		return ErrOwnerDenied
	}
	switch hooks.Input.Mode {
	case "create":
		if entity.ShouldDelete {
			return fmt.Errorf("create cannot delete a job")
		}
		if previous != nil {
			return ErrAlreadyExists
		}
		if hasRunLink(entity) || !validJobStatus(entity.Status) {
			return ErrInvalidTransition
		}
		return nil
	case "submit":
		if previous != nil {
			return ErrAlreadyExists
		}
		if entity.ShouldDelete || !completeRunLink(entity) ||
			strings.TrimSpace(entity.ArtifactRef) != "report-run://"+jobText(entity.ReportRunId) ||
			strings.TrimSpace(entity.Format) != "pdf" || strings.TrimSpace(entity.Scope) != "draft" ||
			entity.Status != "queued" || jobText(entity.ConversationId) == "" ||
			jobText(entity.WorkspaceId) != "" || len(entity.MetadataJson) != 0 ||
			jobText(entity.ArtifactId) != "" || jobText(entity.ErrorText) != "" ||
			len(entity.DiagnosticsJson) != 0 || entity.StartedAt != nil || entity.CompletedAt != nil ||
			entity.RetentionTtlSec != 0 {
			return ErrConflict
		}
		return nil
	case "update":
		if previous == nil {
			return ErrNotFound
		}
		if entity.ShouldDelete || hasRunLink(previous) || hasRunLink(entity) || !validJobStatus(entity.Status) {
			return ErrInvalidTransition
		}
		return nil
	case "claim", "complete", "fail":
		if previous == nil {
			return ErrNotFound
		}
		if entity.ShouldDelete || hooks.Input.Has == nil || !hooks.Input.Has.ExpectedStatus {
			return ErrInvalidTransition
		}
		expected, next := "queued", "running"
		if hooks.Input.Mode == "complete" {
			expected, next = "running", "succeeded"
		} else if hooks.Input.Mode == "fail" {
			expected, next = "running", "failed"
		}
		if previous.Status != expected || hooks.Input.ExpectedStatus != expected || entity.Status != next {
			return ErrInvalidTransition
		}
		if (hooks.Input.Mode == "claim" && entity.StartedAt == nil) || (hooks.Input.Mode != "claim" && entity.CompletedAt == nil) {
			return ErrInvalidTransition
		}
		if hooks.Input.Mode == "complete" && jobText(entity.ArtifactId) == "" {
			return ErrInvalidTransition
		}
		if !allowedTransitionFields(entity, hooks.Input.Mode) {
			return ErrInvalidTransition
		}
		return nil
	case "delete":
		if !entity.ShouldDelete {
			return fmt.Errorf("delete mode requires job delete marker")
		}
		return nil
	default:
		return fmt.Errorf("unsupported report export job mode %q", hooks.Input.Mode)
	}
}

func validJobStatus(value string) bool {
	switch strings.TrimSpace(value) {
	case "queued", "running", "succeeded", "failed":
		return true
	}
	return false
}

func jobText(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func hasRunLink(job *Job) bool {
	return job != nil && (jobText(job.ReportRunId) != "" || job.ReportRunRevision != nil || jobText(job.ExportRequestId) != "")
}

func completeRunLink(job *Job) bool {
	return job != nil && jobText(job.ReportRunId) != "" && job.ReportRunRevision != nil && *job.ReportRunRevision > 0 && jobText(job.ExportRequestId) != "" && len(jobText(job.ExportRequestId)) <= 128
}

func allowedTransitionFields(job *Job, mode string) bool {
	if job == nil || job.Has == nil {
		return false
	}
	h := job.Has
	if h.ArtifactRef || h.ConversationId || h.WorkspaceId || h.AuthContextRef || h.Format || h.Scope ||
		h.ReportSpecJson || h.ReportFillJson || h.ReportPrintJson || h.MetadataJson || h.SubmittedAt ||
		h.ReportRunId || h.ReportRunRevision || h.ExportRequestId {
		return false
	}
	switch mode {
	case "claim":
		return !h.ArtifactId && !h.ErrorText && !h.DiagnosticsJson && !h.CompletedAt && !h.RetentionTtlSec
	case "complete":
		return !h.StartedAt
	case "fail":
		return !h.StartedAt && !h.ArtifactId && !h.RetentionTtlSec
	}
	return false
}
func (hooks *Lifecycle) Validate(ctx context.Context, entity *Job, state xhandler.LifecycleContext[Job, xhandler.NoParent, Output]) error {
	return nil
}
func (hooks *Lifecycle) AfterSequence(ctx context.Context, entity *Job, state xhandler.LifecycleContext[Job, xhandler.NoParent, Output]) error {
	return nil
}
func (hooks *Lifecycle) AfterQueue(ctx context.Context, entity *Job, state xhandler.LifecycleContext[Job, xhandler.NoParent, Output]) error {
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
		copy.ReportSpecJson = cloneJobBytes(row.ReportSpecJson)
		copy.ReportFillJson = cloneJobBytes(row.ReportFillJson)
		copy.ReportPrintJson = cloneJobBytes(row.ReportPrintJson)
		copy.MetadataJson = cloneJobBytes(row.MetadataJson)
		copy.DiagnosticsJson = cloneJobBytes(row.DiagnosticsJson)
		copy.Has = nil
		output.Data[i] = &copy
	}
	return nil
}

func cloneJobBytes(value []byte) []byte {
	if value == nil {
		return nil
	}
	return append([]byte(nil), value...)
}
