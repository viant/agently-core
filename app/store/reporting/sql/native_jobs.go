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
	authctx "github.com/viant/agently-core/internal/auth"
	jobread "github.com/viant/agently-core/internal/datly/reporting/job/read"
	jobwrite "github.com/viant/agently-core/internal/datly/reporting/job/write"
	reportjob "github.com/viant/agently-core/pkg/agently/reportjob"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/spec"
)

var jobWriterTarget = dexec.ComponentTarget{
	Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[jobwrite.WriterComponent]().PkgPath(), Name: "writer"},
	Route:     spec.RouteRef{Method: "PATCH", Path: "/v1/internal/forge/reporting/job"},
}

func (s *Store) readJobsNative(ctx context.Context, input *jobread.Input) ([]*jobread.Job, error) {
	if s == nil || s.native == nil {
		return nil, fmt.Errorf("native reporting runtime is required")
	}
	value, err := s.native.InvokeComponent(native.WithAccess(ctx, native.Access{Internal: hasInternalAccess(ctx)}), dexec.ComponentRequest{Target: jobReaderTarget, Input: input})
	if err != nil {
		return nil, err
	}
	out, ok := value.(*jobread.Output)
	if !ok || out == nil {
		return nil, fmt.Errorf("report job reader returned %T", value)
	}
	return out.Data, nil
}

func (s *Store) getJobNative(ctx context.Context, jobID string) (*reportjob.Record, error) {
	if strings.TrimSpace(jobID) == "" || effectiveOwnerID(ctx) == "" && !hasInternalAccess(ctx) {
		return nil, errNotFound
	}
	input := &jobread.Input{}
	input.SetJobID(strings.TrimSpace(jobID))
	rows, err := s.readJobsNative(ctx, input)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, errNotFound
	}
	if len(rows) != 1 || rows[0] == nil {
		return nil, fmt.Errorf("report job lookup returned %d rows", len(rows))
	}
	return reportJobFromNative(rows[0]), nil
}

func (s *Store) listJobsNative(ctx context.Context) ([]*reportjob.Record, error) {
	if effectiveOwnerID(ctx) == "" && !hasInternalAccess(ctx) {
		return []*reportjob.Record{}, nil
	}
	rows, err := s.readJobsNative(ctx, &jobread.Input{})
	if err != nil {
		return nil, err
	}
	result := make([]*reportjob.Record, 0, len(rows))
	for _, row := range rows {
		if row != nil {
			result = append(result, reportJobFromNative(row))
		}
	}
	return result, nil
}

func (s *Store) writeJobNative(ctx context.Context, mode string, row *jobwrite.Job, expectedStatus string) error {
	if s == nil || s.native == nil {
		return fmt.Errorf("native reporting runtime is required")
	}
	input := &jobwrite.Input{}
	input.SetMode(mode)
	if expectedStatus != "" {
		input.SetExpectedStatus(expectedStatus)
	}
	input.SetJobs([]*jobwrite.Job{row})
	if hasInternalAccess(ctx) && effectiveOwnerID(ctx) == "" && row != nil && strings.TrimSpace(row.OwnerId) != "" {
		ctx = authctx.WithUserInfo(ctx, &authctx.UserInfo{Subject: row.OwnerId})
	}
	value, err := s.native.InvokeComponent(native.WithAccess(ctx, native.Access{Internal: hasInternalAccess(ctx)}), dexec.ComponentRequest{Target: jobWriterTarget, Input: input})
	if err != nil {
		switch {
		case errors.Is(err, jobwrite.ErrNotFound), errors.Is(err, jobwrite.ErrOwnerDenied):
			return errNotFound
		case errors.Is(err, jobwrite.ErrAlreadyExists):
			return reportstore.ErrAlreadyExists
		case errors.Is(err, jobwrite.ErrConflict):
			return reportstore.ErrConflict
		case errors.Is(err, jobwrite.ErrInvalidTransition):
			return reportstore.ErrInvalidTransition
		default:
			return err
		}
	}
	if _, ok := value.(*jobwrite.Output); !ok {
		return fmt.Errorf("report job writer returned %T", value)
	}
	return nil
}

func (s *Store) createJobNative(ctx context.Context, job *reportjob.Record) error {
	if job == nil {
		return errNotFound
	}
	if reportstore.HasRunExportLink(job) {
		return reportstore.ErrInvalidTransition
	}
	if owner := effectiveOwnerID(ctx); !hasInternalAccess(ctx) && (owner == "" || owner != strings.TrimSpace(job.OwnerID)) {
		return errNotFound
	}
	return s.writeJobNative(ctx, "create", nativeJobRow(job), "")
}

func (s *Store) updateJobNative(ctx context.Context, job *reportjob.Record) error {
	if job == nil {
		return errNotFound
	}
	if reportstore.HasRunExportLink(job) {
		return reportstore.ErrInvalidTransition
	}
	if owner := effectiveOwnerID(ctx); !hasInternalAccess(ctx) && (owner == "" || owner != strings.TrimSpace(job.OwnerID)) {
		return errNotFound
	}
	return s.writeJobNative(ctx, "update", nativeJobRow(job), "")
}

func (s *Store) claimJobNative(ctx context.Context, jobID string, startedAt time.Time) (*reportjob.Record, error) {
	job, err := s.getJobNative(ctx, jobID)
	if err != nil {
		return nil, err
	}
	row := &jobwrite.Job{}
	row.SetJobId(job.JobID)
	row.SetOwnerId(job.OwnerID)
	row.SetStatus("running")
	at := startedAt.UTC()
	row.SetStartedAt(&at)
	if err := s.writeJobNative(ctx, "claim", row, "queued"); err != nil {
		return nil, err
	}
	return s.getJobNative(ctx, jobID)
}

func (s *Store) failJobNative(ctx context.Context, jobID, errorText string, diagnostics []byte, completedAt time.Time) (*reportjob.Record, error) {
	job, err := s.getJobNative(ctx, jobID)
	if err != nil {
		return nil, err
	}
	row := &jobwrite.Job{}
	row.SetJobId(job.JobID)
	row.SetOwnerId(job.OwnerID)
	row.SetStatus("failed")
	text := strings.TrimSpace(errorText)
	row.SetErrorText(&text)
	row.SetDiagnosticsJson(append([]byte(nil), diagnostics...))
	at := completedAt.UTC()
	row.SetCompletedAt(&at)
	if err := s.writeJobNative(ctx, "fail", row, "running"); err != nil {
		return nil, err
	}
	return s.getJobNative(ctx, jobID)
}

func nativeJobRow(job *reportjob.Record) *jobwrite.Job {
	row := &jobwrite.Job{}
	row.SetJobId(strings.TrimSpace(job.JobID))
	row.SetArtifactRef(strings.TrimSpace(job.ArtifactRef))
	row.SetOwnerId(strings.TrimSpace(job.OwnerID))
	row.SetConversationId(optionalJobText(job.ConversationID))
	row.SetWorkspaceId(optionalJobText(job.WorkspaceID))
	row.SetAuthContextRef(optionalJobText(job.AuthContextRef))
	row.SetFormat(strings.TrimSpace(job.Format))
	row.SetScope(strings.TrimSpace(job.Scope))
	row.SetStatus(strings.TrimSpace(job.Status))
	row.SetReportSpecJson(append([]byte(nil), job.ReportSpec...))
	row.SetReportFillJson(append([]byte(nil), job.ReportFill...))
	row.SetReportPrintJson(append([]byte(nil), job.ReportPrint...))
	row.SetMetadataJson(append([]byte(nil), job.Metadata...))
	row.SetArtifactId(optionalJobText(job.ArtifactID))
	row.SetErrorText(optionalJobText(job.Error))
	row.SetDiagnosticsJson(append([]byte(nil), job.Diagnostics...))
	row.SetSubmittedAt(job.SubmittedAt.UTC())
	row.SetStartedAt(job.StartedAt)
	row.SetCompletedAt(job.CompletedAt)
	row.SetRetentionTtlSec(int64(job.RetentionTTL / time.Second))
	return row
}

func optionalJobText(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}
