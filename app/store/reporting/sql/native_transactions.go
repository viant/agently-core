package sql

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/viant/agently-core/app/store/native"
	reportstore "github.com/viant/agently-core/app/store/reporting"
	"github.com/viant/agently-core/internal/auth"
	jobread "github.com/viant/agently-core/internal/datly/reporting/job/read"
	adoption "github.com/viant/agently-core/internal/store/reporting/adoption"
	contextstore "github.com/viant/agently-core/internal/store/reporting/context"
	exportcomplete "github.com/viant/agently-core/internal/store/reporting/exportcomplete"
	exportsubmit "github.com/viant/agently-core/internal/store/reporting/exportsubmit"
	runstore "github.com/viant/agently-core/internal/store/reporting/run"
	reportartifact "github.com/viant/agently-core/pkg/agently/reportartifact"
	reportcontext "github.com/viant/agently-core/pkg/agently/reportcontext"
	reportjob "github.com/viant/agently-core/pkg/agently/reportjob"
	reportrun "github.com/viant/agently-core/pkg/agently/reportrun"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/spec"
)

var adoptionTarget = dexec.ComponentTarget{
	Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[adoption.Component]().PkgPath(), Name: "ReportAdoption"},
	Route:     spec.RouteRef{Method: "POST", Path: "/v1/internal/forge/reporting/adopt"},
}

var exportSubmitTarget = dexec.ComponentTarget{
	Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[exportsubmit.Component]().PkgPath(), Name: "RunExportSubmit"},
	Route:     spec.RouteRef{Method: "POST", Path: "/v1/internal/forge/reporting/export/submit"},
}

var exportCompleteTarget = dexec.ComponentTarget{
	Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[exportcomplete.Component]().PkgPath(), Name: "ExportComplete"},
	Route:     spec.RouteRef{Method: "POST", Path: "/v1/internal/forge/reporting/export/complete"},
}

var jobReaderTarget = dexec.ComponentTarget{
	Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[jobread.ReaderComponent]().PkgPath(), Name: "reader"},
	Route:     spec.RouteRef{Method: "GET", Path: "/v1/internal/forge/reporting/job"},
}

func (s *Store) nativeRunStore() *runstore.Store {
	return &runstore.Store{Invoker: s.native, OwnerID: effectiveOwnerID}
}

func (s *Store) nativeContextStore() *contextstore.Store {
	return &contextstore.Store{Invoker: s.native, OwnerID: effectiveOwnerID}
}

func mapNativeRunError(err error) error {
	switch {
	case errors.Is(err, runstore.ErrNotFound):
		return reportstore.ErrNotFound
	case errors.Is(err, runstore.ErrAlreadyExists):
		return reportstore.ErrAlreadyExists
	case errors.Is(err, runstore.ErrCASMismatch):
		return reportstore.ErrCASMismatch
	case errors.Is(err, runstore.ErrImmutable):
		return reportstore.ErrImmutable
	default:
		return err
	}
}

func mapNativeContextError(err error) error {
	switch {
	case errors.Is(err, contextstore.ErrNotFound):
		return reportstore.ErrNotFound
	case errors.Is(err, contextstore.ErrCASMismatch):
		return reportstore.ErrCASMismatch
	default:
		return err
	}
}

func (s *Store) adoptReportRunNative(ctx context.Context, run *reportrun.Record, expectedRunRevision int64, pointer *reportcontext.Record, expectedContextRevision int64) error {
	// These records have the same fields; the conversion keeps all snapshot and
	// CAS fields while the private component owns reads, writers and the unit.
	nativeRun := runstore.Record(*run)
	nativePointer := contextstore.Record(*pointer)
	value, err := s.native.InvokeComponent(native.WithAccess(ctx, native.Access{Internal: false}), dexec.ComponentRequest{
		Target: adoptionTarget,
		Input:  &adoption.Input{Run: &nativeRun, Context: &nativePointer, ExpectedRunRevision: expectedRunRevision, ExpectedContextRevision: expectedContextRevision},
	})
	if err != nil {
		switch {
		case errors.Is(err, adoption.ErrNotFound):
			return reportstore.ErrNotFound
		case errors.Is(err, adoption.ErrCASMismatch):
			return reportstore.ErrCASMismatch
		case errors.Is(err, adoption.ErrImmutable):
			return reportstore.ErrImmutable
		default:
			return err
		}
	}
	if _, ok := value.(*adoption.Output); !ok {
		return fmt.Errorf("report adoption returned %T", value)
	}
	return nil
}

func (s *Store) submitJobNative(ctx context.Context, candidate *reportjob.Record) (*reportjob.Record, bool, error) {
	value, err := s.native.InvokeComponent(native.WithAccess(ctx, native.Access{Internal: false}), dexec.ComponentRequest{
		Target: exportSubmitTarget,
		Input: &exportsubmit.Input{
			JobID: candidate.JobID, OwnerID: candidate.OwnerID, ConversationID: candidate.ConversationID,
			ReportRunID: candidate.ReportRunID, ExportRequestID: candidate.ExportRequestID,
			ArtifactRef: candidate.ArtifactRef, Format: candidate.Format, Scope: candidate.Scope,
			Status: candidate.Status, AuthContextRef: candidate.AuthContextRef,
			SubmittedAt: candidate.SubmittedAt, ReportRunRevision: candidate.ReportRunRevision,
			WorkspaceID: candidate.WorkspaceID, Metadata: candidate.Metadata,
			ArtifactID: candidate.ArtifactID, ErrorText: candidate.Error,
			Diagnostics: candidate.Diagnostics, StartedAt: candidate.StartedAt,
			CompletedAt: candidate.CompletedAt, RetentionTTL: candidate.RetentionTTL,
		},
	})
	if err != nil {
		switch {
		case errors.Is(err, exportsubmit.ErrNotFound):
			return nil, false, reportstore.ErrNotFound
		case errors.Is(err, exportsubmit.ErrConflict):
			return nil, false, reportstore.ErrConflict
		case errors.Is(err, exportsubmit.ErrInvalidTransition):
			return nil, false, reportstore.ErrInvalidTransition
		case errors.Is(err, exportsubmit.ErrAlreadyExists):
			return nil, false, reportstore.ErrAlreadyExists
		default:
			return nil, false, err
		}
	}
	out, ok := value.(*exportsubmit.Output)
	if !ok || out == nil || out.Job == nil {
		return nil, false, fmt.Errorf("export submit returned %T", value)
	}
	return reportJobFromNative(out.Job), out.Replay, nil
}

func reportJobFromNative(job *jobread.Job) *reportjob.Record {
	if job == nil {
		return nil
	}
	return &reportjob.Record{
		JobID: job.JobId, ArtifactRef: job.ArtifactRef, OwnerID: job.OwnerId,
		ConversationID: job.ConversationId, WorkspaceID: job.WorkspaceId,
		AuthContextRef: job.AuthContextRef, Format: job.Format, Scope: job.Scope,
		Status: job.Status, ReportRunID: job.ReportRunId,
		ReportRunRevision: job.ReportRunRevision, ExportRequestID: job.ExportRequestId,
		ReportSpec:  append([]byte(nil), job.ReportSpecJson...),
		ReportFill:  append([]byte(nil), job.ReportFillJson...),
		ReportPrint: append([]byte(nil), job.ReportPrintJson...),
		Metadata:    append([]byte(nil), job.MetadataJson...), ArtifactID: job.ArtifactId,
		Error: job.ErrorText, Diagnostics: append([]byte(nil), job.DiagnosticsJson...),
		SubmittedAt: job.SubmittedAt, StartedAt: job.StartedAt, CompletedAt: job.CompletedAt,
		RetentionTTL: time.Duration(job.RetentionTtlSec) * time.Second,
	}
}

func (s *Store) completeJobNative(ctx context.Context, jobID string, artifact *reportartifact.Record, diagnostics []byte, completedAt time.Time, retentionTTL time.Duration, internal bool) (*reportjob.Record, error) {
	callCtx := native.WithAccess(ctx, native.Access{Internal: false})
	if internal {
		query := &jobread.Input{}
		query.SetJobID(strings.TrimSpace(jobID))
		value, err := s.native.InvokeComponent(native.WithAccess(ctx, native.Access{Internal: true}), dexec.ComponentRequest{Target: jobReaderTarget, Input: query})
		if err != nil {
			return nil, err
		}
		out, ok := value.(*jobread.Output)
		if !ok || out == nil {
			return nil, fmt.Errorf("export job reader returned %T", value)
		}
		if len(out.Data) != 1 || out.Data[0] == nil || strings.TrimSpace(out.Data[0].OwnerId) == "" {
			return nil, reportstore.ErrNotFound
		}
		callCtx = auth.WithUserInfo(callCtx, &auth.UserInfo{Subject: out.Data[0].OwnerId})
	}
	value, err := s.native.InvokeComponent(callCtx, dexec.ComponentRequest{Target: exportCompleteTarget, Input: &exportcomplete.Input{
		JobID: strings.TrimSpace(jobID), ArtifactID: artifact.ArtifactID,
		ContentType: artifact.ContentType, Data: append([]byte(nil), artifact.Data...),
		ArtifactCreatedAt: artifact.CreatedAt, Diagnostics: append([]byte(nil), diagnostics...),
		CompletedAt: completedAt, RetentionTTL: retentionTTL,
	}})
	if err != nil {
		switch {
		case errors.Is(err, exportcomplete.ErrNotFound):
			return nil, reportstore.ErrNotFound
		case errors.Is(err, exportcomplete.ErrConflict):
			return nil, reportstore.ErrConflict
		case errors.Is(err, exportcomplete.ErrInvalidTransition):
			return nil, reportstore.ErrInvalidTransition
		default:
			return nil, err
		}
	}
	out, ok := value.(*exportcomplete.Output)
	if !ok || out == nil || out.Job == nil {
		return nil, fmt.Errorf("export completion returned %T", value)
	}
	return reportJobFromNative(out.Job), nil
}
