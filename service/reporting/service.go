package reporting

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/google/uuid"
	svc "github.com/viant/agently-core/protocol/tool/service"
	authctx "github.com/viant/agently-core/service/auth"
)

var (
	// ErrNotFound hides artifact/job existence across principals.
	ErrNotFound = errors.New("reporting: not found")
)

const Name = "reporting"

// Options configures a reporting Service.
type Options struct {
	Compiler Compiler
	Exporter Exporter
	Store    Store
	Audit    AuditSink
	Now      func() time.Time
	NewID    func() string
}

// Service is the agently-core runtime boundary for reporting compile and
// export job orchestration.
type Service struct {
	compiler Compiler
	exporter Exporter
	store    Store
	audit    AuditSink
	now      func() time.Time
	newID    func() string
}

// New constructs a reporting Service.
func New(opts Options) *Service {
	if opts.Store == nil {
		panic("reporting.New: store is required")
	}
	nowFn := opts.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	newIDFn := opts.NewID
	if newIDFn == nil {
		newIDFn = func() string { return uuid.NewString() }
	}
	return &Service{
		compiler: opts.Compiler,
		exporter: opts.Exporter,
		store:    opts.Store,
		audit:    opts.Audit,
		now:      nowFn,
		newID:    newIDFn,
	}
}

func (s *Service) Name() string { return Name }

func (s *Service) Methods() svc.Signatures {
	return []svc.Signature{
		{
			Name:        "compile",
			Description: "Compile an authored reporting artifact into a canonical ReportSpec.",
			Input:       reflect.TypeOf(&CompileRequest{}),
			Output:      reflect.TypeOf(&CompileResult{}),
		},
		{
			Name:        "submit_export",
			Description: "Submit an async reporting export job against canonical report artifacts.",
			Input:       reflect.TypeOf(&SubmitExportRequest{}),
			Output:      reflect.TypeOf(&ExportJob{}),
		},
		{
			Name:        "get_export_status",
			Description: "Return the current export job state visible to the current principal.",
			Input:       reflect.TypeOf(&GetExportStatusInput{}),
			Output:      reflect.TypeOf(&ExportJob{}),
		},
		{
			Name:        "get_artifact",
			Description: "Return a completed reporting artifact visible to the current principal.",
			Input:       reflect.TypeOf(&GetArtifactInput{}),
			Output:      reflect.TypeOf(&Artifact{}),
		},
		{
			Name:        "start_export",
			Description: "Mark a queued export job running. Intended for internal worker orchestration.",
			Internal:    true,
			Input:       reflect.TypeOf(&StartExportInput{}),
			Output:      reflect.TypeOf(&ExportJob{}),
		},
		{
			Name:        "complete_export",
			Description: "Persist a finished export artifact and mark the job succeeded. Intended for internal worker orchestration.",
			Internal:    true,
			Input:       reflect.TypeOf(&CompleteExportRequest{}),
			Output:      reflect.TypeOf(&ExportJob{}),
		},
		{
			Name:        "fail_export",
			Description: "Mark an export job failed with structured diagnostics. Intended for internal worker orchestration.",
			Internal:    true,
			Input:       reflect.TypeOf(&FailExportRequest{}),
			Output:      reflect.TypeOf(&ExportJob{}),
		},
	}
}

func (s *Service) Method(name string) (svc.Executable, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "compile":
		return s.compileTool, nil
	case "submit_export":
		return s.submitExportTool, nil
	case "get_export_status":
		return s.getExportStatusTool, nil
	case "get_artifact":
		return s.getArtifactTool, nil
	case "start_export":
		return s.startExportTool, nil
	case "complete_export":
		return s.completeExportTool, nil
	case "fail_export":
		return s.failExportTool, nil
	default:
		return nil, svc.NewMethodNotFoundError(name)
	}
}

type GetExportStatusInput struct {
	JobID string `json:"jobId,omitempty"`
}

type GetArtifactInput struct {
	ArtifactID string `json:"artifactId,omitempty"`
}

type StartExportInput struct {
	JobID string `json:"jobId,omitempty"`
}

// Compile runs the configured canonical compiler.
func (s *Service) Compile(ctx context.Context, request *CompileRequest) (*CompileResult, error) {
	if request == nil {
		return nil, fmt.Errorf("reporting compile: request is required")
	}
	if s.compiler == nil {
		return nil, fmt.Errorf("reporting compile: compiler not configured")
	}
	if len(bytes.TrimSpace(request.Document)) == 0 {
		return nil, fmt.Errorf("reporting compile: document is required")
	}
	result, err := s.compiler.Compile(ctx, cloneCompileRequest(request))
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, fmt.Errorf("reporting compile: compiler returned nil result")
	}
	next := cloneCompileResult(result)
	if next.CompiledAt.IsZero() {
		next.CompiledAt = s.now().UTC()
	}
	s.recordAudit(ctx, &AuditEvent{
		EventType:   "report.compile",
		ArtifactRef: strings.TrimSpace(next.ArtifactRef),
		ActorID:     effectiveActorID(ctx),
		OccurredAt:  s.now().UTC(),
		Metadata: map[string]interface{}{
			"sourceKind": strings.TrimSpace(request.SourceKind),
		},
	})
	return next, nil
}

func (s *Service) compileTool(ctx context.Context, in, out interface{}) error {
	input, ok := in.(*CompileRequest)
	if !ok {
		return svc.NewInvalidInputError(in)
	}
	output, ok := out.(*CompileResult)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	result, err := s.Compile(ctx, input)
	if err != nil {
		return err
	}
	*output = *cloneCompileResult(result)
	return nil
}

// SubmitExport enqueues a canonical export job.
func (s *Service) SubmitExport(ctx context.Context, request *SubmitExportRequest) (*ExportJob, error) {
	if request == nil {
		return nil, fmt.Errorf("reporting export: request is required")
	}
	normalizedRequest, err := normalizeSubmitExportRequest(request)
	if err != nil {
		return nil, err
	}
	ownerID := effectiveActorID(ctx)
	if ownerID == "" {
		return nil, fmt.Errorf("reporting export: effective user id is required")
	}
	if err := validateSubmitExportRequest(normalizedRequest); err != nil {
		return nil, err
	}
	scope := normalizedRequest.Scope
	if scope == "" {
		scope = ExportScopeDraft
	}
	job := &ExportJob{
		JobID:       s.newID(),
		ArtifactRef: strings.TrimSpace(normalizedRequest.ArtifactRef),
		OwnerID:     ownerID,
		Format:      normalizedRequest.Format,
		Scope:       scope,
		Status:      JobStatusQueued,
		ReportSpec:  cloneJSON(normalizedRequest.ReportSpec),
		ReportFill:  cloneJSON(normalizedRequest.ReportFill),
		ReportPrint: cloneJSON(normalizedRequest.ReportPrint),
		SubmittedAt: s.now().UTC(),
	}
	if err := s.store.CreateJob(ctx, job); err != nil {
		return nil, err
	}
	s.recordAudit(ctx, &AuditEvent{
		EventType:   "report.export.submit",
		ArtifactRef: job.ArtifactRef,
		JobID:       job.JobID,
		ActorID:     ownerID,
		OccurredAt:  s.now().UTC(),
		Metadata: map[string]interface{}{
			"format": string(job.Format),
			"scope":  string(job.Scope),
		},
	})
	return cloneJob(job), nil
}

func (s *Service) submitExportTool(ctx context.Context, in, out interface{}) error {
	input, ok := in.(*SubmitExportRequest)
	if !ok {
		return svc.NewInvalidInputError(in)
	}
	output, ok := out.(*ExportJob)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	job, err := s.SubmitExport(ctx, input)
	if err != nil {
		return err
	}
	*output = *cloneJob(job)
	return nil
}

// RunExport executes a queued job through the configured exporter boundary and
// persists either a completed artifact or a failed job state.
func (s *Service) RunExport(ctx context.Context, jobID string) (*ExportJob, error) {
	if s.exporter == nil {
		return nil, fmt.Errorf("reporting export execution: exporter is not configured")
	}
	job, err := s.StartExport(ctx, jobID)
	if err != nil {
		return nil, err
	}
	if err := validateSubmitExportRequest(&SubmitExportRequest{
		ArtifactRef: job.ArtifactRef,
		Format:      job.Format,
		Scope:       job.Scope,
		ReportSpec:  cloneJSON(job.ReportSpec),
		ReportFill:  cloneJSON(job.ReportFill),
		ReportPrint: cloneJSON(job.ReportPrint),
	}); err != nil {
		return s.failRunExport(ctx, job.JobID, err)
	}

	result, err := s.exporter.Export(ctx, &RenderRequest{
		JobID:       job.JobID,
		ArtifactRef: job.ArtifactRef,
		OwnerID:     job.OwnerID,
		Format:      job.Format,
		Scope:       job.Scope,
		ReportSpec:  cloneJSON(job.ReportSpec),
		ReportFill:  cloneJSON(job.ReportFill),
		ReportPrint: cloneJSON(job.ReportPrint),
	})
	if err != nil {
		return s.failRunExport(ctx, job.JobID, err)
	}
	if result == nil {
		return s.failRunExport(ctx, job.JobID, fmt.Errorf("reporting export execution: exporter returned nil result"))
	}
	if len(result.Data) == 0 {
		return s.failRunExport(ctx, job.JobID, fmt.Errorf("reporting export execution: exporter returned empty artifact data"))
	}
	completed, err := s.CompleteExport(ctx, &CompleteExportRequest{
		JobID:        job.JobID,
		ContentType:  strings.TrimSpace(result.ContentType),
		Data:         append([]byte{}, result.Data...),
		Diagnostics:  cloneDiagnostics(result.Diagnostics),
		RetentionTTL: result.RetentionTTL,
	})
	if err != nil {
		return nil, err
	}
	return completed, nil
}

// StartExport marks a queued job running. Intended for async workers.
func (s *Service) StartExport(ctx context.Context, jobID string) (*ExportJob, error) {
	job, err := s.store.GetJob(ctx, strings.TrimSpace(jobID))
	if err != nil {
		return nil, err
	}
	if job == nil {
		return nil, ErrNotFound
	}
	if job.Status != JobStatusQueued {
		return nil, fmt.Errorf("reporting export: job %s is not queued", strings.TrimSpace(jobID))
	}
	startedAt := s.now().UTC()
	job.Status = JobStatusRunning
	job.StartedAt = &startedAt
	if err := s.store.UpdateJob(ctx, job); err != nil {
		return nil, err
	}
	return cloneJob(job), nil
}

func (s *Service) startExportTool(ctx context.Context, in, out interface{}) error {
	input, ok := in.(*StartExportInput)
	if !ok {
		return svc.NewInvalidInputError(in)
	}
	output, ok := out.(*ExportJob)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	job, err := s.StartExport(ctx, strings.TrimSpace(input.JobID))
	if err != nil {
		return err
	}
	*output = *cloneJob(job)
	return nil
}

// CompleteExport persists a finished export artifact and marks the job
// succeeded.
func (s *Service) CompleteExport(ctx context.Context, request *CompleteExportRequest) (*ExportJob, error) {
	if request == nil {
		return nil, fmt.Errorf("reporting export completion: request is required")
	}
	job, err := s.store.GetJob(ctx, strings.TrimSpace(request.JobID))
	if err != nil {
		return nil, err
	}
	if job == nil {
		return nil, ErrNotFound
	}
	if len(request.Data) == 0 {
		return nil, fmt.Errorf("reporting export completion: artifact data is required")
	}
	contentType := strings.TrimSpace(request.ContentType)
	if contentType == "" {
		contentType = defaultContentType(job.Format)
	}
	artifact := &Artifact{
		ArtifactID:   s.newID(),
		JobID:        job.JobID,
		ArtifactRef:  job.ArtifactRef,
		OwnerID:      job.OwnerID,
		Format:       job.Format,
		ContentType:  contentType,
		Data:         append([]byte{}, request.Data...),
		CreatedAt:    s.now().UTC(),
		RetentionTTL: request.RetentionTTL,
	}
	if err := s.store.PutArtifact(ctx, artifact); err != nil {
		return nil, err
	}
	completedAt := s.now().UTC()
	job.Status = JobStatusSucceeded
	job.ArtifactID = artifact.ArtifactID
	job.Diagnostics = cloneDiagnostics(request.Diagnostics)
	job.CompletedAt = &completedAt
	job.RetentionTTL = request.RetentionTTL
	if err := s.store.UpdateJob(ctx, job); err != nil {
		return nil, err
	}
	s.recordAudit(ctx, &AuditEvent{
		EventType:   "report.export.complete",
		ArtifactRef: job.ArtifactRef,
		JobID:       job.JobID,
		ArtifactID:  artifact.ArtifactID,
		ActorID:     effectiveActorID(ctx),
		OccurredAt:  s.now().UTC(),
		Metadata: map[string]interface{}{
			"format":       string(job.Format),
			"contentType":  artifact.ContentType,
			"retentionTtl": artifact.RetentionTTL.String(),
		},
	})
	return cloneJob(job), nil
}

func (s *Service) completeExportTool(ctx context.Context, in, out interface{}) error {
	input, ok := in.(*CompleteExportRequest)
	if !ok {
		return svc.NewInvalidInputError(in)
	}
	output, ok := out.(*ExportJob)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	job, err := s.CompleteExport(ctx, input)
	if err != nil {
		return err
	}
	*output = *cloneJob(job)
	return nil
}

// FailExport marks an export job failed.
func (s *Service) FailExport(ctx context.Context, request *FailExportRequest) (*ExportJob, error) {
	if request == nil {
		return nil, fmt.Errorf("reporting export failure: request is required")
	}
	job, err := s.store.GetJob(ctx, strings.TrimSpace(request.JobID))
	if err != nil {
		return nil, err
	}
	if job == nil {
		return nil, ErrNotFound
	}
	completedAt := s.now().UTC()
	job.Status = JobStatusFailed
	job.Error = strings.TrimSpace(request.Error)
	job.Diagnostics = cloneDiagnostics(request.Diagnostics)
	job.CompletedAt = &completedAt
	if err := s.store.UpdateJob(ctx, job); err != nil {
		return nil, err
	}
	s.recordAudit(ctx, &AuditEvent{
		EventType:   "report.export.fail",
		ArtifactRef: job.ArtifactRef,
		JobID:       job.JobID,
		ActorID:     effectiveActorID(ctx),
		OccurredAt:  s.now().UTC(),
		Metadata: map[string]interface{}{
			"format": string(job.Format),
			"error":  job.Error,
		},
	})
	return cloneJob(job), nil
}

func (s *Service) failExportTool(ctx context.Context, in, out interface{}) error {
	input, ok := in.(*FailExportRequest)
	if !ok {
		return svc.NewInvalidInputError(in)
	}
	output, ok := out.(*ExportJob)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	job, err := s.FailExport(ctx, input)
	if err != nil {
		return err
	}
	*output = *cloneJob(job)
	return nil
}

// GetExportStatus returns a job visible to the current principal.
func (s *Service) GetExportStatus(ctx context.Context, jobID string) (*ExportJob, error) {
	job, err := s.store.GetJob(ctx, strings.TrimSpace(jobID))
	if err != nil {
		return nil, err
	}
	if job == nil {
		return nil, ErrNotFound
	}
	if !isVisibleToActor(ctx, job.GetOwnerID()) {
		return nil, ErrNotFound
	}
	return cloneJob(job), nil
}

func (s *Service) getExportStatusTool(ctx context.Context, in, out interface{}) error {
	input, ok := in.(*GetExportStatusInput)
	if !ok {
		return svc.NewInvalidInputError(in)
	}
	output, ok := out.(*ExportJob)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	job, err := s.GetExportStatus(ctx, strings.TrimSpace(input.JobID))
	if err != nil {
		return err
	}
	*output = *cloneJob(job)
	return nil
}

// GetArtifact returns an artifact visible to the current principal.
func (s *Service) GetArtifact(ctx context.Context, artifactID string) (*Artifact, error) {
	artifact, err := s.store.GetArtifact(ctx, strings.TrimSpace(artifactID))
	if err != nil {
		return nil, err
	}
	if artifact == nil {
		return nil, ErrNotFound
	}
	if !isVisibleToActor(ctx, artifact.GetOwnerID()) {
		return nil, ErrNotFound
	}
	return cloneArtifact(artifact), nil
}

func (s *Service) getArtifactTool(ctx context.Context, in, out interface{}) error {
	input, ok := in.(*GetArtifactInput)
	if !ok {
		return svc.NewInvalidInputError(in)
	}
	output, ok := out.(*Artifact)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	artifact, err := s.GetArtifact(ctx, strings.TrimSpace(input.ArtifactID))
	if err != nil {
		return err
	}
	*output = *cloneArtifact(artifact)
	return nil
}

func (s *Service) failRunExport(ctx context.Context, jobID string, exportErr error) (*ExportJob, error) {
	failed, failErr := s.FailExport(ctx, &FailExportRequest{
		JobID: strings.TrimSpace(jobID),
		Error: strings.TrimSpace(exportErr.Error()),
	})
	if failErr != nil {
		return nil, failErr
	}
	return failed, exportErr
}

func defaultContentType(format ExportFormat) string {
	switch format {
	case ExportFormatPDF:
		return "application/pdf"
	case ExportFormatCSV:
		return "text/csv"
	case ExportFormatXLSX:
		return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	default:
		return "application/octet-stream"
	}
}

func (s *Service) recordAudit(ctx context.Context, event *AuditEvent) {
	if s == nil || s.audit == nil || event == nil {
		return
	}
	_ = s.audit.Record(ctx, cloneAuditEvent(event))
}

func effectiveActorID(ctx context.Context) string {
	return strings.TrimSpace(authctx.EffectiveUserID(ctx))
}

func isVisibleToActor(ctx context.Context, ownerID string) bool {
	actorID := effectiveActorID(ctx)
	normalizedOwner := strings.TrimSpace(ownerID)
	if normalizedOwner == "" {
		return actorID == ""
	}
	return actorID != "" && actorID == normalizedOwner
}

func cloneJSON(value json.RawMessage) json.RawMessage {
	if len(value) == 0 {
		return nil
	}
	out := make([]byte, len(value))
	copy(out, value)
	return out
}

func cloneDiagnostics(input []Diagnostic) []Diagnostic {
	if len(input) == 0 {
		return nil
	}
	out := make([]Diagnostic, len(input))
	copy(out, input)
	return out
}

func cloneCompileRequest(input *CompileRequest) *CompileRequest {
	if input == nil {
		return nil
	}
	return &CompileRequest{
		ArtifactRef: strings.TrimSpace(input.ArtifactRef),
		SourceKind:  strings.TrimSpace(input.SourceKind),
		Document:    cloneJSON(input.Document),
	}
}

func cloneCompileResult(input *CompileResult) *CompileResult {
	if input == nil {
		return nil
	}
	return &CompileResult{
		ArtifactRef: strings.TrimSpace(input.ArtifactRef),
		ReportSpec:  cloneJSON(input.ReportSpec),
		Diagnostics: cloneDiagnostics(input.Diagnostics),
		CompiledAt:  input.CompiledAt,
	}
}

func cloneSubmitExportRequest(input *SubmitExportRequest) *SubmitExportRequest {
	if input == nil {
		return nil
	}
	var reportExportRequest *ReportExportRequest
	if input.ReportExportRequest != nil {
		reportExportRequest = &ReportExportRequest{
			Version:     input.ReportExportRequest.Version,
			Kind:        strings.TrimSpace(input.ReportExportRequest.Kind),
			Target:      input.ReportExportRequest.Target,
			Source:      input.ReportExportRequest.Source,
			ReportSpec:  cloneJSON(input.ReportExportRequest.ReportSpec),
			ReportFill:  cloneJSON(input.ReportExportRequest.ReportFill),
			ReportPrint: cloneJSON(input.ReportExportRequest.ReportPrint),
		}
	}
	return &SubmitExportRequest{
		ArtifactRef:         strings.TrimSpace(input.ArtifactRef),
		Format:              input.Format,
		Scope:               input.Scope,
		ReportSpec:          cloneJSON(input.ReportSpec),
		ReportFill:          cloneJSON(input.ReportFill),
		ReportPrint:         cloneJSON(input.ReportPrint),
		ReportExportRequest: reportExportRequest,
	}
}

func cloneJob(input *ExportJob) *ExportJob {
	if input == nil {
		return nil
	}
	out := &ExportJob{
		JobID:        strings.TrimSpace(input.JobID),
		ArtifactRef:  strings.TrimSpace(input.ArtifactRef),
		OwnerID:      strings.TrimSpace(input.OwnerID),
		Format:       input.Format,
		Scope:        input.Scope,
		Status:       input.Status,
		ReportSpec:   cloneJSON(input.ReportSpec),
		ReportFill:   cloneJSON(input.ReportFill),
		ReportPrint:  cloneJSON(input.ReportPrint),
		ArtifactID:   strings.TrimSpace(input.ArtifactID),
		Error:        strings.TrimSpace(input.Error),
		Diagnostics:  cloneDiagnostics(input.Diagnostics),
		SubmittedAt:  input.SubmittedAt,
		RetentionTTL: input.RetentionTTL,
	}
	if input.StartedAt != nil {
		startedAt := *input.StartedAt
		out.StartedAt = &startedAt
	}
	if input.CompletedAt != nil {
		completedAt := *input.CompletedAt
		out.CompletedAt = &completedAt
	}
	return out
}

func cloneArtifact(input *Artifact) *Artifact {
	if input == nil {
		return nil
	}
	out := &Artifact{
		ArtifactID:   strings.TrimSpace(input.ArtifactID),
		JobID:        strings.TrimSpace(input.JobID),
		ArtifactRef:  strings.TrimSpace(input.ArtifactRef),
		OwnerID:      strings.TrimSpace(input.OwnerID),
		Format:       input.Format,
		ContentType:  strings.TrimSpace(input.ContentType),
		CreatedAt:    input.CreatedAt,
		RetentionTTL: input.RetentionTTL,
	}
	if len(input.Data) > 0 {
		out.Data = append([]byte{}, input.Data...)
	}
	return out
}

func cloneAuditEvent(input *AuditEvent) *AuditEvent {
	if input == nil {
		return nil
	}
	out := &AuditEvent{
		EventType:   strings.TrimSpace(input.EventType),
		ArtifactRef: strings.TrimSpace(input.ArtifactRef),
		JobID:       strings.TrimSpace(input.JobID),
		ArtifactID:  strings.TrimSpace(input.ArtifactID),
		ActorID:     strings.TrimSpace(input.ActorID),
		OccurredAt:  input.OccurredAt,
	}
	if len(input.Metadata) > 0 {
		out.Metadata = make(map[string]interface{}, len(input.Metadata))
		for key, value := range input.Metadata {
			out.Metadata[key] = value
		}
	}
	return out
}

func (j *ExportJob) GetOwnerID() string {
	if j == nil {
		return ""
	}
	return j.OwnerID
}

func (a *Artifact) GetOwnerID() string {
	if a == nil {
		return ""
	}
	return a.OwnerID
}
