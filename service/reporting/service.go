package reporting

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/viant/afs"
	afsscratchpad "github.com/viant/afs/scratchpad"
	reportstore "github.com/viant/agently-core/app/store/reporting"
	iauth "github.com/viant/agently-core/internal/auth"
	tokenctx "github.com/viant/agently-core/internal/auth/token"
	exportrequest "github.com/viant/agently-core/pkg/agently/exportrequest"
	asynccfg "github.com/viant/agently-core/protocol/async"
	svc "github.com/viant/agently-core/protocol/tool/service"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	authctx "github.com/viant/agently-core/service/auth"
)

var (
	// ErrNotFound hides artifact/job existence across principals.
	ErrNotFound = errors.New("reporting: not found")
	// ErrJobNotQueued indicates a queued worker claim race or invalid lifecycle
	// transition when a caller tries to start a non-queued export job.
	ErrJobNotQueued = errors.New("reporting: job not queued")
	// ErrAlreadyExists indicates a storage collision on a generated reporting ID.
	ErrAlreadyExists = errors.New("reporting: already exists")
	// ErrConflict indicates an idempotency replay with different immutable
	// run-reference input.
	ErrConflict = errors.New("reporting: conflict")
)

const Name = "reporting"

// Options configures a reporting Service.
type Options struct {
	Compiler Compiler
	Exporter Exporter
	Store    Store
	// ActiveRunResolver is wired only when durable browser-run persistence and
	// reporting orchestration are both enabled.
	ActiveRunResolver ActiveReportRunResolver
	Audit             AuditSink
	Scratchpad        *afsscratchpad.Service
	ScratchpadFS      afs.Service
	TokenProvider     tokenctx.Provider
	Now               func() time.Time
	NewID             func() string
	// ExportFromRunEnabled gates only the T2 run-reference submission mode.
	// Legacy payload submission remains available when this is false.
	ExportFromRunEnabled bool
	// ConversationAdoptionEnabled allows the active-run resolver to expose an
	// adopted completed manual run when its full durable snapshot is valid.
	ConversationAdoptionEnabled bool
}

// Service is the agently-core runtime boundary for reporting compile and
// export job orchestration.
type Service struct {
	compiler                    Compiler
	exporter                    Exporter
	store                       Store
	audit                       AuditSink
	scratchpad                  *afsscratchpad.Service
	scratchpadFS                afs.Service
	tokenProvider               tokenctx.Provider
	now                         func() time.Time
	newID                       func() string
	exportFromRunEnabled        bool
	conversationAdoptionEnabled bool
	activeRunResolver           ActiveReportRunResolver
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
		compiler:      opts.Compiler,
		exporter:      opts.Exporter,
		store:         opts.Store,
		audit:         opts.Audit,
		scratchpad:    opts.Scratchpad,
		tokenProvider: opts.TokenProvider,
		scratchpadFS: func() afs.Service {
			if opts.ScratchpadFS != nil {
				return opts.ScratchpadFS
			}
			return afs.New()
		}(),
		now:                         nowFn,
		newID:                       newIDFn,
		exportFromRunEnabled:        opts.ExportFromRunEnabled,
		conversationAdoptionEnabled: opts.ConversationAdoptionEnabled,
		activeRunResolver:           normalizeActiveReportRunResolver(opts.ActiveRunResolver),
	}
}

func (s *Service) Name() string { return Name }

func (s *Service) AsyncConfig(toolName string) *asynccfg.Config {
	for _, config := range s.AsyncConfigs() {
		if config == nil {
			continue
		}
		if strings.TrimSpace(config.Run.Tool) == strings.TrimSpace(toolName) ||
			strings.TrimSpace(config.Status.Tool) == strings.TrimSpace(toolName) {
			return config
		}
	}
	return nil
}

// AsyncConfigs links exact export submission jobs to exact status reads. The
// generic async runtime owns the bounded wait; no reporting-specific wait tool
// or model-driven polling loop is exposed.
func (s *Service) AsyncConfigs() []*asynccfg.Config {
	return []*asynccfg.Config{
		{
			DefaultExecutionMode:       string(asynccfg.ExecutionModeWait),
			TerminalCarrierBeforeModel: true,
			TimeoutMs:                  int((300 * time.Second) / time.Millisecond),
			PollIntervalMs:             int((500 * time.Millisecond) / time.Millisecond),
			Narration:                  "none",
			Run: asynccfg.RunConfig{
				Tool:            "reporting:submit_export",
				OperationIDPath: "jobId",
				Selector: &asynccfg.Selector{
					StatusPath: "status",
					DataPath:   ".",
				},
			},
			Status: asynccfg.StatusConfig{
				Tool:           "reporting:get_export_status",
				OperationIDArg: "jobId",
				Selector: asynccfg.Selector{
					StatusPath: "status",
					DataPath:   ".",
					TerminalStatuses: []string{
						string(JobStatusSucceeded),
						string(JobStatusFailed),
					},
				},
			},
		},
	}
}

func (s *Service) Methods() svc.Signatures {
	methods := []svc.Signature{
		{
			Name:        "compile",
			Description: "Compile an authored reporting artifact into a canonical ReportSpec.",
			Input:       reflect.TypeOf(&CompileRequest{}),
			Output:      reflect.TypeOf(&CompileResult{}),
		},
		{
			Name:        "compile_fenced_report",
			Description: "Compile committed forge-report and forge-data fences on the backend into canonical ReportSpec, ReportFill, and ReportPrint. The returned reportExportRequest is immediately exportable: pass it unchanged as reporting:submit_export.reportExportRequest.",
			Input:       reflect.TypeOf(&CompileFencedReportRequest{}),
			Output:      reflect.TypeOf(&CompileFencedReportResult{}),
		},
		{
			Name:        "compile_and_export_fenced_report",
			Description: "Compile committed forge-report and forge-data fences and synchronously export the result. Prefer this compact one-call path when no interactive report-builder UI is attached; it returns job and downloadable artifact metadata without report payloads or artifact bytes.",
			Input:       reflect.TypeOf(&CompileAndExportFencedReportRequest{}),
			Output:      reflect.TypeOf(&CompileAndExportFencedReportResult{}),
		},
		{
			Name:        "record_audit_event",
			Description: "Record a structured reporting audit event through the configured reporting audit sink.",
			Input:       reflect.TypeOf(&RecordAuditEventInput{}),
			Output:      reflect.TypeOf(&AuditEvent{}),
		},
		{
			Name:        "share_artifact",
			Description: "Create or return a shared reporting artifact such as a saved view using the reporting persistence boundary.",
			Input:       reflect.TypeOf(&ShareArtifactRequest{}),
			Output:      reflect.TypeOf(&SharedArtifact{}),
		},
		{
			Name:        "transition_artifact",
			Description: "Apply a lifecycle transition to an existing reporting artifact or materialize a published snapshot when canonical payloads are supplied.",
			Input:       reflect.TypeOf(&TransitionArtifactRequest{}),
			Output:      reflect.TypeOf(&SharedArtifact{}),
		},
		{
			Name:        "export_report",
			Description: "Submit an async reporting export job against canonical report artifacts.",
			Input:       reflect.TypeOf(&SubmitExportRequest{}),
			Output:      reflect.TypeOf(&ExportJob{}),
		},
		{
			Name:        "submit_export",
			Description: "Submit an async reporting export job. Use exactly one input form: an exact completed reportRunId plus PDF format, a unified source (report, preset, or inline), or a fully materialized reportExportRequest; never mix forms. Only for one logical serial hosted report-to-email delivery attempt: call this tool at most once; with wait execution, preserve the exact jobId and consume either a direct terminal ExportJob or an AwaitTerminal result containing exactly one item; and do not request status after any delivered terminal observation, requesting it at most once only when none was delivered. A new explicit user export request may start a new export operation and logical attempt.",
			Input:       reflect.TypeOf(&SubmitExportRequest{}),
			Output:      reflect.TypeOf(&ExportJob{}),
		},
		{
			Name:        "get_export_status",
			Description: "Return the compact current export job lifecycle status visible to the current principal.",
			Input:       reflect.TypeOf(&GetExportStatusInput{}),
			Output:      reflect.TypeOf(&ExportJobStatus{}),
		},
		{
			Name:        "list_export_jobs",
			Description: "List export jobs visible to the current principal with optional report and lifecycle filters.",
			Input:       reflect.TypeOf(&ListExportJobsInput{}),
			Output:      reflect.TypeOf(&ListExportJobsResult{}),
		},
		{
			Name:        "list_export_artifacts",
			Description: "List export artifacts visible to the current principal with optional report and format filters.",
			Input:       reflect.TypeOf(&ListExportArtifactsInput{}),
			Output:      reflect.TypeOf(&ListExportArtifactsResult{}),
		},
		{
			Name:        "get_artifact",
			Description: "Return a completed reporting artifact visible to the current principal. Data bytes are omitted by default; set includeData=true only for a trusted download path.",
			Input:       reflect.TypeOf(&GetArtifactInput{}),
			Output:      reflect.TypeOf(&Artifact{}),
		},
		{
			Name:        "get_shared_artifact",
			Description: "Return a shared reporting artifact visible to the current principal.",
			Input:       reflect.TypeOf(&GetSharedArtifactInput{}),
			Output:      reflect.TypeOf(&SharedArtifact{}),
		},
		{
			Name:        "list_shared_artifacts",
			Description: "List shared reporting artifacts visible to the current principal with optional report and lifecycle filters.",
			Input:       reflect.TypeOf(&ListSharedArtifactsInput{}),
			Output:      reflect.TypeOf(&ListSharedArtifactsResult{}),
		},
		{
			Name:        "save_report",
			Description: "Persist a reusable report record owned by the current principal.",
			Input:       reflect.TypeOf(&SaveReportRequest{}),
			Output:      reflect.TypeOf(&SharedArtifact{}),
		},
		{
			Name:        "get_report",
			Description: "Load a persisted report record visible to the current principal.",
			Input:       reflect.TypeOf(&GetReportInput{}),
			Output:      reflect.TypeOf(&SharedArtifact{}),
		},
		{
			Name:        "list_reports",
			Description: "List persisted report records visible to the current principal.",
			Input:       reflect.TypeOf(&ListReportsInput{}),
			Output:      reflect.TypeOf(&ListReportsResult{}),
		},
		{
			Name:        "update_report",
			Description: "Update a persisted report record visible to the current principal.",
			Input:       reflect.TypeOf(&UpdateReportRequest{}),
			Output:      reflect.TypeOf(&SharedArtifact{}),
		},
		{
			Name:        "duplicate_report",
			Description: "Duplicate a user-owned persisted report under a new report identity.",
			Input:       reflect.TypeOf(&DuplicateReportRequest{}),
			Output:      reflect.TypeOf(&SharedArtifact{}),
		},
		{
			Name:        "delete_report",
			Description: "Permanently delete a user-owned persisted report. Built-in presets are not reports and cannot be deleted.",
			Input:       reflect.TypeOf(&DeleteReportRequest{}),
			Output:      reflect.TypeOf(&DeleteReportResult{}),
		},
		{
			Name:        "record_report_run",
			Description: "Record the most recent successful execution time for a user-owned persisted report.",
			Input:       reflect.TypeOf(&RecordReportRunRequest{}),
			Output:      reflect.TypeOf(&SharedArtifact{}),
		},
		{
			Name:        "run_export",
			Description: "Execute a queued export job through the configured exporter boundary. Intended for internal worker orchestration.",
			Internal:    true,
			Input:       reflect.TypeOf(&RunExportInput{}),
			Output:      reflect.TypeOf(&ExportJob{}),
		},
		{
			Name:        "run_queued_exports",
			Description: "Execute queued export jobs through the configured exporter boundary. Intended for internal worker orchestration.",
			Internal:    true,
			Input:       reflect.TypeOf(&RunQueuedExportsInput{}),
			Output:      reflect.TypeOf(&RunQueuedExportsResult{}),
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
	if s.activeRunResolver != nil {
		description := "Return the exact completed prompt-origin report run that is active for the authenticated actor and trusted current conversation. The input must be an empty object."
		if s.conversationAdoptionEnabled {
			description = "Return the exact eligible completed prompt- or manual-origin report run that is active for the authenticated actor and trusted current conversation. The input must be an empty object."
		}
		methods = append(methods, svc.Signature{
			Name:        "get_active_report_run",
			Description: description,
			Input:       reflect.TypeOf(&GetActiveReportRunInput{}),
			Output:      reflect.TypeOf(&ActiveReportRun{}),
		})
	}
	return methods
}

func (s *Service) Method(name string) (svc.Executable, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "compile":
		return s.compileTool, nil
	case "compile_fenced_report":
		return s.compileFencedReportTool, nil
	case "compile_and_export_fenced_report":
		return s.compileAndExportFencedReportTool, nil
	case "record_audit_event":
		return s.recordAuditEventTool, nil
	case "share_artifact":
		return s.shareArtifactTool, nil
	case "transition_artifact":
		return s.transitionArtifactTool, nil
	case "export_report":
		return s.submitExportTool, nil
	case "submit_export":
		return s.submitExportTool, nil
	case "get_export_status":
		return s.getExportStatusTool, nil
	case "list_export_jobs":
		return s.listExportJobsTool, nil
	case "list_export_artifacts":
		return s.listExportArtifactsTool, nil
	case "get_artifact":
		return s.getArtifactTool, nil
	case "get_shared_artifact":
		return s.getSharedArtifactTool, nil
	case "list_shared_artifacts":
		return s.listSharedArtifactsTool, nil
	case "save_report":
		return s.saveReportTool, nil
	case "get_report":
		return s.getReportTool, nil
	case "list_reports":
		return s.listReportsTool, nil
	case "update_report":
		return s.updateReportTool, nil
	case "duplicate_report":
		return s.duplicateReportTool, nil
	case "delete_report":
		return s.deleteReportTool, nil
	case "record_report_run":
		return s.recordReportRunTool, nil
	case "get_active_report_run":
		if s.activeRunResolver == nil {
			return nil, svc.NewMethodNotFoundError(name)
		}
		return s.getActiveReportRunTool, nil
	case "run_export":
		return s.runExportTool, nil
	case "run_queued_exports":
		return s.runQueuedExportsTool, nil
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

type ListExportJobsInput struct {
	ArtifactRef string       `json:"artifactRef,omitempty"`
	Format      ExportFormat `json:"format,omitempty"`
	Scope       ExportScope  `json:"scope,omitempty"`
	Status      JobStatus    `json:"status,omitempty"`
	Limit       int          `json:"limit,omitempty"`
}

type ListExportJobsResult struct {
	Jobs       []*ExportJob `json:"jobs,omitempty"`
	TotalCount int          `json:"totalCount,omitempty"`
}

type ListExportArtifactsInput struct {
	ArtifactRef string       `json:"artifactRef,omitempty"`
	JobID       string       `json:"jobId,omitempty"`
	Format      ExportFormat `json:"format,omitempty"`
	Limit       int          `json:"limit,omitempty"`
}

type ListExportArtifactsResult struct {
	Artifacts  []*Artifact `json:"artifacts,omitempty"`
	TotalCount int         `json:"totalCount,omitempty"`
}

type GetArtifactInput struct {
	ArtifactID  string `json:"artifactId,omitempty"`
	IncludeData bool   `json:"includeData,omitempty"`
}

type GetSharedArtifactInput struct {
	ArtifactID string `json:"artifactId,omitempty"`
}

type ListSharedArtifactsInput struct {
	ArtifactRef string `json:"artifactRef,omitempty"`
	ReportID    string `json:"reportId,omitempty"`
	Kind        string `json:"kind,omitempty"`
	Lifecycle   string `json:"lifecycle,omitempty"`
	Limit       int    `json:"limit,omitempty"`
}

type ListSharedArtifactsResult struct {
	Artifacts  []*SharedArtifact `json:"artifacts,omitempty"`
	TotalCount int               `json:"totalCount,omitempty"`
}

type RunExportInput struct {
	JobID string `json:"jobId,omitempty"`
}

type RunQueuedExportsInput struct {
	Limit int `json:"limit,omitempty"`
}

type RunQueuedExportsResult struct {
	Jobs           []*ExportJob `json:"jobs,omitempty"`
	ProcessedCount int          `json:"processedCount,omitempty"`
	SucceededCount int          `json:"succeededCount,omitempty"`
	FailedCount    int          `json:"failedCount,omitempty"`
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

// RecordAuditEvent validates and records a structured reporting audit event.
func (s *Service) RecordAuditEvent(ctx context.Context, input *RecordAuditEventInput) (*AuditEvent, error) {
	if input == nil || input.Event == nil {
		return nil, fmt.Errorf("reporting audit: event is required")
	}
	if s.audit == nil {
		return nil, fmt.Errorf("reporting audit: audit sink is not configured")
	}
	event := cloneAuditEvent(input.Event)
	event.EventType = strings.TrimSpace(event.EventType)
	event.ArtifactRef = strings.TrimSpace(event.ArtifactRef)
	event.JobID = strings.TrimSpace(event.JobID)
	event.ArtifactID = strings.TrimSpace(event.ArtifactID)
	event.ActorID = strings.TrimSpace(event.ActorID)
	event.ActorRef = strings.TrimSpace(event.ActorRef)
	if event.EventType == "" {
		return nil, fmt.Errorf("reporting audit: eventType is required")
	}
	if event.ArtifactRef == "" {
		return nil, fmt.Errorf("reporting audit: artifactRef is required")
	}
	if event.Version < 0 {
		return nil, fmt.Errorf("reporting audit: version must be >= 0")
	}
	if event.ActorRef == "" {
		event.ActorRef = strings.TrimSpace(effectiveActorID(ctx))
	}
	if event.ActorID == "" {
		event.ActorID = event.ActorRef
	}
	if event.ActorRef == "" && event.ActorID == "" {
		return nil, fmt.Errorf("reporting audit: actor identity is required")
	}
	if event.OccurredAt.IsZero() {
		event.OccurredAt = s.now().UTC()
	} else {
		event.OccurredAt = event.OccurredAt.UTC()
	}
	if err := s.audit.Record(ctx, event); err != nil {
		return nil, err
	}
	return cloneAuditEvent(event), nil
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

func (s *Service) recordAuditEventTool(ctx context.Context, in, out interface{}) error {
	input, ok := in.(*RecordAuditEventInput)
	if !ok {
		return svc.NewInvalidInputError(in)
	}
	output, ok := out.(*AuditEvent)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	event, err := s.RecordAuditEvent(ctx, input)
	if err != nil {
		return err
	}
	*output = *cloneAuditEvent(event)
	return nil
}

// ShareArtifact creates or returns a shared artifact such as a saved view.
func (s *Service) ShareArtifact(ctx context.Context, request *ShareArtifactRequest) (*SharedArtifact, error) {
	if request == nil {
		return nil, fmt.Errorf("reporting lifecycle: share request is required")
	}
	ownerID := effectiveActorID(ctx)
	if ownerID == "" {
		return nil, fmt.Errorf("reporting lifecycle: effective user id is required")
	}
	normalized, err := normalizeShareArtifactRequest(request)
	if err != nil {
		return nil, err
	}
	if existing, err := s.findVisibleSharedArtifactByRef(ctx, normalized.ArtifactRef); err == nil && existing != nil {
		return cloneSharedArtifact(existing), nil
	}
	if normalized.ReportExportRequest == nil {
		return nil, fmt.Errorf("reporting lifecycle: shared artifact payload is required to create a saved view")
	}
	envelope := normalized.ReportExportRequest
	sourceArtifactID := normalizeSharedArtifactSourceID("saved_view", envelope.Source.SourceArtifactID, envelope.Source.ReportID, s.newID())
	artifact := &SharedArtifact{
		ArtifactID:       s.newID(),
		ArtifactRef:      buildSharedArtifactRef("reportBuilder.savedView", sourceArtifactID),
		OwnerID:          ownerID,
		OwnerRef:         buildOwnerRef(ownerID),
		Kind:             "reportBuilder.savedView",
		Lifecycle:        normalizeSharedArtifactLifecycle(normalized.Lifecycle, "draft"),
		Version:          resolveSharedArtifactVersion(normalized.Version, envelope.Source.DocumentVersion),
		ReportID:         strings.TrimSpace(envelope.Source.ReportID),
		Title:            strings.TrimSpace(envelope.Source.Title),
		SourceArtifactID: sourceArtifactID,
		BaseArtifactRef:  strings.TrimSpace(normalized.ArtifactRef),
		DocumentVersion:  envelope.Source.DocumentVersion,
		Document:         cloneJSON(normalized.ReportDocument),
		ReportSpec:       cloneJSON(envelope.ReportSpec),
		ReportFill:       cloneJSON(envelope.ReportFill),
		ReportPrint:      cloneJSON(envelope.ReportPrint),
		SavedViewOverlay: cloneJSON(normalized.SavedViewOverlay),
		Metadata:         cloneJSON(normalized.Metadata),
		CreatedAt:        s.now().UTC(),
	}
	if err := s.store.CreateSharedArtifact(ctx, artifact); err != nil {
		return nil, err
	}
	return cloneSharedArtifact(artifact), nil
}

func (s *Service) shareArtifactTool(ctx context.Context, in, out interface{}) error {
	input, ok := in.(*ShareArtifactRequest)
	if !ok {
		return svc.NewInvalidInputError(in)
	}
	output, ok := out.(*SharedArtifact)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	artifact, err := s.ShareArtifact(ctx, input)
	if err != nil {
		return err
	}
	*output = *cloneSharedArtifact(artifact)
	return nil
}

// TransitionArtifact mutates lifecycle state or materializes a published snapshot.
func (s *Service) TransitionArtifact(ctx context.Context, request *TransitionArtifactRequest) (*SharedArtifact, error) {
	if request == nil {
		return nil, fmt.Errorf("reporting lifecycle: transition request is required")
	}
	ownerID := effectiveActorID(ctx)
	if ownerID == "" {
		return nil, fmt.Errorf("reporting lifecycle: effective user id is required")
	}
	normalized, err := normalizeTransitionArtifactRequest(request)
	if err != nil {
		return nil, err
	}
	targetLifecycle := normalizeSharedArtifactLifecycle(normalized.To, "")
	if targetLifecycle == "" {
		return nil, fmt.Errorf("reporting lifecycle: transition target is required")
	}
	existing, err := s.findVisibleSharedArtifactByRef(ctx, normalized.ArtifactRef)
	if err == nil && existing != nil {
		updated := cloneSharedArtifact(existing)
		updated.Lifecycle = targetLifecycle
		updated.Version = resolveSharedArtifactVersion(normalized.Version, updated.Version)
		if targetLifecycle == "published" && strings.TrimSpace(updated.Kind) == "" {
			updated.Kind = "reportBuilder.publishedSnapshot"
		}
		updatedAt := s.now().UTC()
		updated.UpdatedAt = &updatedAt
		if err := s.store.UpdateSharedArtifact(ctx, updated); err != nil {
			return nil, err
		}
		return cloneSharedArtifact(updated), nil
	}
	if targetLifecycle != "published" {
		return nil, ErrNotFound
	}
	if normalized.ReportExportRequest == nil {
		return nil, fmt.Errorf("reporting lifecycle: canonical export payload is required to publish a new snapshot")
	}
	envelope := normalized.ReportExportRequest
	sourceArtifactID := normalizeSharedArtifactSourceID("published_snapshot", envelope.Source.SourceArtifactID, envelope.Source.ReportID, s.newID())
	artifact := &SharedArtifact{
		ArtifactID:       s.newID(),
		ArtifactRef:      buildSharedArtifactRef("reportBuilder.publishedSnapshot", sourceArtifactID),
		OwnerID:          ownerID,
		OwnerRef:         buildOwnerRef(ownerID),
		Kind:             "reportBuilder.publishedSnapshot",
		Lifecycle:        "published",
		Version:          resolveSharedArtifactVersion(normalized.Version, envelope.Source.DocumentVersion),
		ReportID:         strings.TrimSpace(envelope.Source.ReportID),
		Title:            strings.TrimSpace(envelope.Source.Title),
		SourceArtifactID: sourceArtifactID,
		BaseArtifactRef:  strings.TrimSpace(normalized.ArtifactRef),
		DocumentVersion:  envelope.Source.DocumentVersion,
		Document:         cloneJSON(normalized.ReportDocument),
		ReportSpec:       cloneJSON(envelope.ReportSpec),
		ReportFill:       cloneJSON(envelope.ReportFill),
		ReportPrint:      cloneJSON(envelope.ReportPrint),
		Metadata:         cloneJSON(normalized.Metadata),
		CreatedAt:        s.now().UTC(),
	}
	if err := s.store.CreateSharedArtifact(ctx, artifact); err != nil {
		return nil, err
	}
	return cloneSharedArtifact(artifact), nil
}

func (s *Service) transitionArtifactTool(ctx context.Context, in, out interface{}) error {
	input, ok := in.(*TransitionArtifactRequest)
	if !ok {
		return svc.NewInvalidInputError(in)
	}
	output, ok := out.(*SharedArtifact)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	artifact, err := s.TransitionArtifact(ctx, input)
	if err != nil {
		return err
	}
	*output = *cloneSharedArtifact(artifact)
	return nil
}

// SubmitExport enqueues a canonical export job.
func (s *Service) SubmitExport(ctx context.Context, request *SubmitExportRequest) (*ExportJob, error) {
	if request == nil {
		return nil, fmt.Errorf("reporting export: request is required")
	}
	if strings.TrimSpace(request.ReportRunID) != "" {
		return s.submitExportFromRun(ctx, request)
	}
	normalizedRequest, err := s.resolveSubmitExportRequest(ctx, request)
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
		JobID:          s.newID(),
		ArtifactRef:    strings.TrimSpace(normalizedRequest.ArtifactRef),
		OwnerID:        ownerID,
		ConversationID: strings.TrimSpace(normalizedRequest.ConversationID),
		WorkspaceID:    strings.TrimSpace(normalizedRequest.WorkspaceID),
		AuthContextRef: strings.TrimSpace(buildAuthContextRef(ctx)),
		Format:         normalizedRequest.Format,
		Scope:          scope,
		Status:         JobStatusQueued,
		ReportSpec:     cloneJSON(normalizedRequest.ReportSpec),
		ReportFill:     cloneJSON(normalizedRequest.ReportFill),
		ReportPrint:    cloneJSON(normalizedRequest.ReportPrint),
		Metadata:       cloneJSON(normalizedRequest.Metadata),
		SubmittedAt:    s.now().UTC(),
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

func (s *Service) submitExportFromRun(ctx context.Context, request *SubmitExportRequest) (*ExportJob, error) {
	if !s.exportFromRunEnabled {
		return nil, fmt.Errorf("reporting export: run-reference mode is disabled")
	}
	if request == nil {
		return nil, fmt.Errorf("reporting export: request is required")
	}
	reportRunID := strings.TrimSpace(request.ReportRunID)
	if reportRunID == "" {
		return nil, fmt.Errorf("reporting export: reportRunId is required for run-reference mode")
	}
	if request.Format != ExportFormatPDF {
		return nil, fmt.Errorf("reporting export: run-reference mode currently supports pdf only")
	}
	if request.runModeHasAlternateFields ||
		request.Source != nil || request.ReportExportRequest != nil ||
		strings.TrimSpace(request.ArtifactRef) != "" || request.Scope != "" ||
		strings.TrimSpace(request.ConversationID) != "" ||
		strings.TrimSpace(request.WorkspaceID) != "" ||
		len(bytes.TrimSpace(request.ReportSpec)) != 0 ||
		len(bytes.TrimSpace(request.ReportFill)) != 0 ||
		len(bytes.TrimSpace(request.ReportPrint)) != 0 ||
		len(bytes.TrimSpace(request.Metadata)) != 0 {
		return nil, fmt.Errorf("reporting export: run-reference mode does not accept browser/model payload snapshots or legacy source fields")
	}
	ownerID := effectiveActorID(ctx)
	if ownerID == "" {
		return nil, fmt.Errorf("reporting export: effective user id is required")
	}
	exportRequestID := exportrequest.ID(ctx)
	if exportRequestID == "" {
		return nil, fmt.Errorf("reporting export: trusted export request identity is required")
	}
	conversationID := strings.TrimSpace(runtimerequestctx.ConversationIDFromContext(ctx))
	if conversationID == "" {
		return nil, fmt.Errorf("reporting export: trusted current conversation is required")
	}
	runStore, ok := s.store.(RunExportStore)
	if !ok {
		return nil, fmt.Errorf("reporting export: run-reference store is not configured")
	}
	candidate := &ExportJob{
		JobID:           strings.TrimSpace(s.newID()),
		ArtifactRef:     "report-run://" + reportRunID,
		OwnerID:         ownerID,
		ConversationID:  conversationID,
		AuthContextRef:  strings.TrimSpace(buildAuthContextRef(ctx)),
		Format:          ExportFormatPDF,
		Scope:           ExportScopeDraft,
		Status:          JobStatusQueued,
		ReportRunID:     reportRunID,
		ExportRequestID: exportRequestID,
		SubmittedAt:     s.now().UTC(),
	}
	job, replay, err := runStore.SubmitJobFromRun(ctx, candidate)
	if err != nil {
		return nil, err
	}
	if job == nil {
		return nil, fmt.Errorf("reporting export: run-reference store returned an empty job")
	}
	if err := validateSubmitExportRequest(&SubmitExportRequest{
		ArtifactRef: job.ArtifactRef,
		Format:      job.Format,
		Scope:       job.Scope,
		ReportSpec:  cloneJSON(job.ReportSpec),
		ReportFill:  cloneJSON(job.ReportFill),
		ReportPrint: cloneJSON(job.ReportPrint),
	}); err != nil {
		return nil, fmt.Errorf("reporting export: persisted run snapshot is not exportable: %w", err)
	}
	s.recordAudit(ctx, &AuditEvent{
		EventType:   "report.export.submit",
		ArtifactRef: job.ArtifactRef,
		JobID:       job.JobID,
		ActorID:     ownerID,
		OccurredAt:  s.now().UTC(),
		Metadata: map[string]interface{}{
			"format":            string(job.Format),
			"scope":             string(job.Scope),
			"reportRunId":       job.ReportRunID,
			"reportRunRevision": job.ReportRunRevision,
			"exportRequestId":   job.ExportRequestID,
			"replay":            replay,
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
	runCtx, err := s.ensureRunExportAuthContext(ctx, job)
	if err != nil {
		return s.failRunExport(ctx, job.JobID, err)
	}
	if err := validateSubmitExportRequest(&SubmitExportRequest{
		ArtifactRef: job.ArtifactRef,
		Format:      job.Format,
		Scope:       job.Scope,
		ReportSpec:  cloneJSON(job.ReportSpec),
		ReportFill:  cloneJSON(job.ReportFill),
		ReportPrint: cloneJSON(job.ReportPrint),
	}); err != nil {
		return s.failRunExport(runCtx, job.JobID, err)
	}

	result, err := s.exporter.Export(runCtx, &RenderRequest{
		JobID:             job.JobID,
		ArtifactRef:       job.ArtifactRef,
		OwnerID:           job.OwnerID,
		ConversationID:    job.ConversationID,
		WorkspaceID:       job.WorkspaceID,
		AuthContextRef:    job.AuthContextRef,
		Format:            job.Format,
		Scope:             job.Scope,
		ReportRunID:       job.ReportRunID,
		ReportRunRevision: job.ReportRunRevision,
		ReportSpec:        cloneJSON(job.ReportSpec),
		ReportFill:        cloneJSON(job.ReportFill),
		ReportPrint:       cloneJSON(job.ReportPrint),
		Metadata:          cloneJSON(job.Metadata),
	})
	if err != nil {
		return s.failRunExport(runCtx, job.JobID, err)
	}
	if result == nil {
		return s.failRunExport(runCtx, job.JobID, fmt.Errorf("reporting export execution: exporter returned nil result"))
	}
	if len(result.Data) == 0 {
		return s.failRunExport(runCtx, job.JobID, fmt.Errorf("reporting export execution: exporter returned empty artifact data"))
	}
	completed, err := s.CompleteExport(runCtx, &CompleteExportRequest{
		JobID:        job.JobID,
		ContentType:  strings.TrimSpace(result.ContentType),
		Data:         append([]byte{}, result.Data...),
		Diagnostics:  cloneDiagnostics(result.Diagnostics),
		RetentionTTL: result.RetentionTTL,
	})
	if err != nil {
		return completed, err
	}
	return completed, nil
}

func (s *Service) runExportTool(ctx context.Context, in, out interface{}) error {
	input, ok := in.(*RunExportInput)
	if !ok {
		return svc.NewInvalidInputError(in)
	}
	output, ok := out.(*ExportJob)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	job, err := s.RunExport(ctx, strings.TrimSpace(input.JobID))
	if err != nil {
		if job != nil {
			*output = *cloneJob(job)
		}
		return err
	}
	*output = *cloneJob(job)
	return nil
}

// RunQueuedExports executes queued jobs in submitted order up to limit.
func (s *Service) RunQueuedExports(ctx context.Context, limit int) (*RunQueuedExportsResult, error) {
	if limit < 1 {
		return nil, fmt.Errorf("reporting export execution: limit must be >= 1")
	}
	jobs, err := s.store.ListJobs(ctx)
	if err != nil {
		return nil, err
	}
	queued := make([]*ExportJob, 0, len(jobs))
	for _, job := range jobs {
		if job != nil && job.Status == JobStatusQueued {
			queued = append(queued, cloneJob(job))
		}
	}
	sort.SliceStable(queued, func(i, j int) bool {
		if queued[i].SubmittedAt.Equal(queued[j].SubmittedAt) {
			return strings.TrimSpace(queued[i].JobID) < strings.TrimSpace(queued[j].JobID)
		}
		return queued[i].SubmittedAt.Before(queued[j].SubmittedAt)
	})
	result := &RunQueuedExportsResult{
		Jobs: make([]*ExportJob, 0, limit),
	}
	for _, queuedJob := range queued {
		if result.ProcessedCount >= limit {
			break
		}
		job, runErr := s.RunExport(ctx, queuedJob.JobID)
		if runErr != nil && job == nil {
			if errors.Is(runErr, ErrNotFound) || errors.Is(runErr, ErrJobNotQueued) {
				continue
			}
			return result, runErr
		}
		if job != nil {
			result.Jobs = append(result.Jobs, cloneJob(job))
			result.ProcessedCount++
			switch job.Status {
			case JobStatusSucceeded:
				result.SucceededCount++
			case JobStatusFailed:
				result.FailedCount++
			}
		}
	}
	return result, nil
}

// ReconcileStaleRunningExports marks interrupted running jobs failed after a
// bounded timeout. Filesystem stores may instead finish a job when they find
// the artifact durably written before a prior process stopped.
func (s *Service) ReconcileStaleRunningExports(ctx context.Context, staleAfter time.Duration) ([]*ExportJob, error) {
	if staleAfter <= 0 {
		return nil, fmt.Errorf("reporting export reconciliation: staleAfter must be > 0")
	}
	runStore, ok := s.store.(RunExportStore)
	if !ok {
		return []*ExportJob{}, nil
	}
	now := s.now().UTC()
	return runStore.ReconcileRunningJobs(
		ctx,
		now.Add(-staleAfter),
		now,
		"reporting export interrupted while running; submit an explicit retry with a new request",
	)
}

func (s *Service) runQueuedExportsTool(ctx context.Context, in, out interface{}) error {
	input, ok := in.(*RunQueuedExportsInput)
	if !ok {
		return svc.NewInvalidInputError(in)
	}
	output, ok := out.(*RunQueuedExportsResult)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	result, err := s.RunQueuedExports(ctx, input.Limit)
	if err != nil {
		if result != nil {
			*output = *cloneRunQueuedExportsResult(result)
		}
		return err
	}
	*output = *cloneRunQueuedExportsResult(result)
	return nil
}

// StartExport marks a queued job running. Intended for async workers.
func (s *Service) StartExport(ctx context.Context, jobID string) (*ExportJob, error) {
	if runStore, ok := s.store.(RunExportStore); ok {
		job, err := runStore.ClaimJob(ctx, strings.TrimSpace(jobID), s.now().UTC())
		if errors.Is(err, reportstore.ErrInvalidTransition) {
			return nil, fmt.Errorf("reporting export: job %s is not queued: %w", strings.TrimSpace(jobID), ErrJobNotQueued)
		}
		return job, err
	}
	job, err := s.store.GetJob(ctx, strings.TrimSpace(jobID))
	if err != nil {
		return nil, err
	}
	if job == nil {
		return nil, ErrNotFound
	}
	if job.Status != JobStatusQueued {
		return nil, fmt.Errorf("reporting export: job %s is not queued: %w", strings.TrimSpace(jobID), ErrJobNotQueued)
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
	if job.Status != JobStatusRunning {
		if job.Status == JobStatusSucceeded && strings.TrimSpace(job.ArtifactID) != "" {
			artifact, artifactErr := s.store.GetArtifact(ctx, job.ArtifactID)
			if artifactErr == nil && artifact != nil &&
				strings.TrimSpace(artifact.JobID) == strings.TrimSpace(job.JobID) &&
				strings.TrimSpace(artifact.OwnerID) == strings.TrimSpace(job.OwnerID) &&
				artifact.Format == job.Format {
				return cloneJob(job), nil
			}
		}
		return nil, fmt.Errorf("reporting export completion: job %s is not running", strings.TrimSpace(request.JobID))
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
	completionDiagnostics := cloneDiagnostics(request.Diagnostics)
	if err := s.publishArtifactToScratchpad(ctx, artifact); err != nil {
		// Scratchpad is a convenience handoff channel, not the authoritative
		// artifact store. Native clients download the trusted persisted bytes
		// directly, so a temporary external-store/auth failure must not discard
		// an otherwise valid export.
		artifact.SourceURL = ""
		completionDiagnostics = append(completionDiagnostics, Diagnostic{
			Code:         "scratchpad_publish_unavailable",
			Severity:     "warning",
			Message:      "The export completed, but its scratchpad handoff is temporarily unavailable.",
			SuggestedFix: "Download the artifact directly or retry the scratchpad handoff later.",
			Cause:        err.Error(),
		})
	}
	if runStore, ok := s.store.(RunExportStore); ok {
		completedAt := s.now().UTC()
		completed, err := runStore.CompleteJobWithArtifact(
			ctx,
			job.JobID,
			artifact,
			completionDiagnostics,
			completedAt,
			request.RetentionTTL,
		)
		if err != nil {
			return nil, err
		}
		s.recordAudit(ctx, &AuditEvent{
			EventType:   "report.export.complete",
			ArtifactRef: completed.ArtifactRef,
			JobID:       completed.JobID,
			ArtifactID:  completed.ArtifactID,
			ActorID:     effectiveActorID(ctx),
			OccurredAt:  s.now().UTC(),
			Metadata: map[string]interface{}{
				"format":            string(completed.Format),
				"contentType":       artifact.ContentType,
				"retentionTtl":      artifact.RetentionTTL.String(),
				"reportRunId":       completed.ReportRunID,
				"reportRunRevision": completed.ReportRunRevision,
			},
		})
		return cloneJob(completed), nil
	}
	if err := s.store.PutArtifact(ctx, artifact); err != nil {
		if errors.Is(err, ErrAlreadyExists) {
			failed, failErr := s.FailExport(ctx, &FailExportRequest{
				JobID: strings.TrimSpace(request.JobID),
				Error: strings.TrimSpace(err.Error()),
			})
			if failErr != nil {
				return nil, failErr
			}
			return failed, err
		}
		return nil, err
	}
	completedAt := s.now().UTC()
	job.Status = JobStatusSucceeded
	job.ArtifactID = artifact.ArtifactID
	job.Diagnostics = completionDiagnostics
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
		if job != nil {
			*output = *cloneJob(job)
		}
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
	if job.Status != JobStatusRunning {
		return nil, fmt.Errorf("reporting export failure: job %s is not running", strings.TrimSpace(request.JobID))
	}
	completedAt := s.now().UTC()
	if runStore, ok := s.store.(RunExportStore); ok {
		failed, err := runStore.FailJob(
			ctx,
			job.JobID,
			strings.TrimSpace(request.Error),
			cloneDiagnostics(request.Diagnostics),
			completedAt,
		)
		if err != nil {
			return nil, err
		}
		s.recordAudit(ctx, &AuditEvent{
			EventType:   "report.export.fail",
			ArtifactRef: failed.ArtifactRef,
			JobID:       failed.JobID,
			ActorID:     effectiveActorID(ctx),
			OccurredAt:  s.now().UTC(),
			Metadata: map[string]interface{}{
				"format":            string(failed.Format),
				"error":             failed.Error,
				"reportRunId":       failed.ReportRunID,
				"reportRunRevision": failed.ReportRunRevision,
			},
		})
		return cloneJob(failed), nil
	}
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
	if !isExportJobVisible(ctx, job) {
		return nil, ErrNotFound
	}
	return cloneJob(job), nil
}

func (s *Service) getExportStatusTool(ctx context.Context, in, out interface{}) error {
	input, ok := in.(*GetExportStatusInput)
	if !ok {
		return svc.NewInvalidInputError(in)
	}
	output, ok := out.(*ExportJobStatus)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	job, err := s.GetExportStatus(ctx, strings.TrimSpace(input.JobID))
	if err != nil {
		return err
	}
	*output = *exportJobStatus(job)
	return nil
}

// ListExportJobs returns export jobs visible to the current principal.
func (s *Service) ListExportJobs(ctx context.Context, input *ListExportJobsInput) (*ListExportJobsResult, error) {
	actorID := effectiveActorID(ctx)
	if actorID == "" {
		return nil, fmt.Errorf("reporting export listing: effective user id is required")
	}
	jobs, err := s.store.ListJobs(ctx)
	if err != nil {
		return nil, err
	}
	var normalized ListExportJobsInput
	if input != nil {
		normalized = ListExportJobsInput{
			ArtifactRef: strings.TrimSpace(input.ArtifactRef),
			Format:      input.Format,
			Scope:       input.Scope,
			Status:      input.Status,
			Limit:       input.Limit,
		}
	}
	filtered := make([]*ExportJob, 0, len(jobs))
	for _, job := range jobs {
		if job == nil || !isExportJobVisible(ctx, job) {
			continue
		}
		if normalized.ArtifactRef != "" && strings.TrimSpace(job.ArtifactRef) != normalized.ArtifactRef {
			continue
		}
		if normalized.Format != "" && job.Format != normalized.Format {
			continue
		}
		if normalized.Scope != "" && job.Scope != normalized.Scope {
			continue
		}
		if normalized.Status != "" && job.Status != normalized.Status {
			continue
		}
		filtered = append(filtered, cloneJob(job))
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		if filtered[i].SubmittedAt.Equal(filtered[j].SubmittedAt) {
			return strings.TrimSpace(filtered[i].JobID) > strings.TrimSpace(filtered[j].JobID)
		}
		return filtered[i].SubmittedAt.After(filtered[j].SubmittedAt)
	})
	result := &ListExportJobsResult{
		Jobs:       filtered,
		TotalCount: len(filtered),
	}
	if normalized.Limit > 0 && len(result.Jobs) > normalized.Limit {
		result.Jobs = append([]*ExportJob{}, result.Jobs[:normalized.Limit]...)
	}
	return result, nil
}

func (s *Service) listExportJobsTool(ctx context.Context, in, out interface{}) error {
	input, ok := in.(*ListExportJobsInput)
	if !ok {
		return svc.NewInvalidInputError(in)
	}
	output, ok := out.(*ListExportJobsResult)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	result, err := s.ListExportJobs(ctx, input)
	if err != nil {
		return err
	}
	*output = *cloneListExportJobsResult(result)
	return nil
}

// ListExportArtifacts returns export artifacts visible to the current principal.
func (s *Service) ListExportArtifacts(ctx context.Context, input *ListExportArtifactsInput) (*ListExportArtifactsResult, error) {
	actorID := effectiveActorID(ctx)
	if actorID == "" {
		return nil, fmt.Errorf("reporting artifact listing: effective user id is required")
	}
	now := s.now().UTC()
	artifacts, err := s.store.ListArtifacts(ctx)
	if err != nil {
		return nil, err
	}
	jobs, err := s.store.ListJobs(ctx)
	if err != nil {
		return nil, err
	}
	jobsByID := make(map[string]*ExportJob, len(jobs))
	for _, job := range jobs {
		if job == nil {
			continue
		}
		jobsByID[strings.TrimSpace(job.JobID)] = job
	}
	var normalized ListExportArtifactsInput
	if input != nil {
		normalized = ListExportArtifactsInput{
			ArtifactRef: strings.TrimSpace(input.ArtifactRef),
			JobID:       strings.TrimSpace(input.JobID),
			Format:      input.Format,
			Limit:       input.Limit,
		}
	}
	filtered := make([]*Artifact, 0, len(artifacts))
	for _, artifact := range artifacts {
		if artifact == nil {
			continue
		}
		if !isCompletedArtifactVisibleToActor(ctx, artifact, jobsByID[strings.TrimSpace(artifact.JobID)], now) {
			continue
		}
		if normalized.ArtifactRef != "" && strings.TrimSpace(artifact.ArtifactRef) != normalized.ArtifactRef {
			continue
		}
		if normalized.JobID != "" && strings.TrimSpace(artifact.JobID) != normalized.JobID {
			continue
		}
		if normalized.Format != "" && artifact.Format != normalized.Format {
			continue
		}
		expanded, err := s.enrichArtifactWithScratchpad(ctx, artifact)
		if err != nil {
			return nil, err
		}
		// Artifact listings are a metadata/discovery surface. Returning the
		// binary payload here can overflow MCP tool responses for PDFs and
		// duplicates the explicit get_artifact/download path.
		expanded.Data = nil
		filtered = append(filtered, expanded)
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		if filtered[i].CreatedAt.Equal(filtered[j].CreatedAt) {
			return strings.TrimSpace(filtered[i].ArtifactID) > strings.TrimSpace(filtered[j].ArtifactID)
		}
		return filtered[i].CreatedAt.After(filtered[j].CreatedAt)
	})
	result := &ListExportArtifactsResult{
		Artifacts:  filtered,
		TotalCount: len(filtered),
	}
	if normalized.Limit > 0 && len(result.Artifacts) > normalized.Limit {
		result.Artifacts = append([]*Artifact{}, result.Artifacts[:normalized.Limit]...)
	}
	return result, nil
}

func (s *Service) listExportArtifactsTool(ctx context.Context, in, out interface{}) error {
	input, ok := in.(*ListExportArtifactsInput)
	if !ok {
		return svc.NewInvalidInputError(in)
	}
	output, ok := out.(*ListExportArtifactsResult)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	result, err := s.ListExportArtifacts(ctx, input)
	if err != nil {
		return err
	}
	*output = *cloneListExportArtifactsResult(result)
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
	job, err := s.store.GetJob(ctx, strings.TrimSpace(artifact.JobID))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if !isCompletedArtifactVisibleToActor(ctx, artifact, job, s.now().UTC()) {
		return nil, ErrNotFound
	}
	expanded, err := s.enrichArtifactWithScratchpad(ctx, artifact)
	if err == nil {
		return expanded, nil
	}
	// Scratchpad publication is an optional handoff optimization. A completed
	// export already persisted with bytes must remain directly downloadable
	// when the external scratchpad store is temporarily unavailable or
	// misconfigured. This is especially important for native clients, which
	// request the trusted includeData path and do not need a scratchpad URL.
	if len(artifact.Data) > 0 {
		fallback := cloneArtifact(artifact)
		fallback.SourceURL = ""
		return fallback, nil
	}
	return nil, err
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
	if !input.IncludeData {
		output.Data = nil
	}
	return nil
}

// GetSharedArtifact returns a shared artifact visible to the current principal.
func (s *Service) GetSharedArtifact(ctx context.Context, artifactID string) (*SharedArtifact, error) {
	artifact, err := s.store.GetSharedArtifact(ctx, strings.TrimSpace(artifactID))
	if err != nil {
		return nil, err
	}
	if artifact == nil || !isVisibleToActor(ctx, artifact.GetOwnerID()) {
		return nil, ErrNotFound
	}
	return cloneSharedArtifact(artifact), nil
}

func (s *Service) getSharedArtifactTool(ctx context.Context, in, out interface{}) error {
	input, ok := in.(*GetSharedArtifactInput)
	if !ok {
		return svc.NewInvalidInputError(in)
	}
	output, ok := out.(*SharedArtifact)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	artifact, err := s.GetSharedArtifact(ctx, strings.TrimSpace(input.ArtifactID))
	if err != nil {
		return err
	}
	*output = *cloneSharedArtifact(artifact)
	return nil
}

// ListSharedArtifacts returns shared artifacts visible to the current principal.
func (s *Service) ListSharedArtifacts(ctx context.Context, input *ListSharedArtifactsInput) (*ListSharedArtifactsResult, error) {
	actorID := effectiveActorID(ctx)
	if actorID == "" {
		return nil, fmt.Errorf("reporting shared artifact listing: effective user id is required")
	}
	artifacts, err := s.store.ListSharedArtifacts(ctx)
	if err != nil {
		return nil, err
	}
	var normalized ListSharedArtifactsInput
	if input != nil {
		normalized = ListSharedArtifactsInput{
			ArtifactRef: strings.TrimSpace(input.ArtifactRef),
			ReportID:    strings.TrimSpace(input.ReportID),
			Kind:        strings.TrimSpace(input.Kind),
			Lifecycle:   strings.TrimSpace(strings.ToLower(input.Lifecycle)),
			Limit:       input.Limit,
		}
	}
	filtered := make([]*SharedArtifact, 0, len(artifacts))
	for _, artifact := range artifacts {
		if artifact == nil || !isVisibleToActor(ctx, artifact.GetOwnerID()) {
			continue
		}
		if normalized.ArtifactRef != "" && strings.TrimSpace(artifact.ArtifactRef) != normalized.ArtifactRef {
			continue
		}
		if normalized.ReportID != "" && strings.TrimSpace(artifact.ReportID) != normalized.ReportID {
			continue
		}
		if normalized.Kind != "" && strings.TrimSpace(artifact.Kind) != normalized.Kind {
			continue
		}
		if normalized.Lifecycle != "" && strings.TrimSpace(strings.ToLower(artifact.Lifecycle)) != normalized.Lifecycle {
			continue
		}
		filtered = append(filtered, cloneSharedArtifact(artifact))
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		if filtered[i].CreatedAt.Equal(filtered[j].CreatedAt) {
			return strings.TrimSpace(filtered[i].ArtifactID) > strings.TrimSpace(filtered[j].ArtifactID)
		}
		return filtered[i].CreatedAt.After(filtered[j].CreatedAt)
	})
	result := &ListSharedArtifactsResult{
		Artifacts:  filtered,
		TotalCount: len(filtered),
	}
	if normalized.Limit > 0 && len(result.Artifacts) > normalized.Limit {
		result.Artifacts = append([]*SharedArtifact{}, result.Artifacts[:normalized.Limit]...)
	}
	return result, nil
}

func (s *Service) listSharedArtifactsTool(ctx context.Context, in, out interface{}) error {
	input, ok := in.(*ListSharedArtifactsInput)
	if !ok {
		return svc.NewInvalidInputError(in)
	}
	output, ok := out.(*ListSharedArtifactsResult)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	result, err := s.ListSharedArtifacts(ctx, input)
	if err != nil {
		return err
	}
	*output = *cloneListSharedArtifactsResult(result)
	return nil
}

const savedReportArtifactKind = "reportBuilder.savedReportPayload"

func (s *Service) SaveReport(ctx context.Context, request *SaveReportRequest) (*SharedArtifact, error) {
	if request == nil {
		return nil, fmt.Errorf("report store: save request is required")
	}
	ownerID := effectiveActorID(ctx)
	if ownerID == "" {
		return nil, fmt.Errorf("report store: effective user id is required")
	}
	normalized := *request
	normalized.ReportDocument = normalizeToolJSONPayload(request.ReportDocument)
	normalized.ReportSpec = normalizeToolJSONPayload(request.ReportSpec)
	normalized.CompileState = normalizeToolJSONPayload(request.CompileState)
	normalized.ReportFill = normalizeToolJSONPayload(request.ReportFill)
	normalized.ReportPrint = normalizeToolJSONPayload(request.ReportPrint)
	normalized.Metadata = normalizeToolJSONPayload(request.Metadata)

	reportID := strings.TrimSpace(normalized.ReportID)
	if reportID == "" {
		reportID = jsonObjectText(normalized.ReportDocument, "id")
	}
	if reportID == "" {
		reportID = s.newID()
	}
	title := strings.TrimSpace(normalized.Title)
	if title == "" {
		title = jsonObjectText(normalized.ReportDocument, "title")
	}
	if reportID == "" && title == "" && len(bytes.TrimSpace(normalized.ReportDocument)) == 0 && len(bytes.TrimSpace(normalized.ReportSpec)) == 0 {
		return nil, fmt.Errorf("report store: report identity or payload is required")
	}
	normalized.ReportID = reportID
	normalized.Title = title
	if err := validateInlineSaveReport(&normalized); err != nil {
		return nil, err
	}
	sourceArtifactID := normalizeSharedArtifactSourceID("report", "", reportID, "")
	artifactRef := strings.TrimSpace(normalized.ArtifactRef)
	if artifactRef == "" {
		artifactRef = buildSharedArtifactRef(savedReportArtifactKind, sourceArtifactID)
	}
	if title == "" {
		title = reportID
	}
	artifact := &SharedArtifact{
		ArtifactID:       s.newID(),
		ArtifactRef:      artifactRef,
		OwnerID:          ownerID,
		OwnerRef:         buildOwnerRef(ownerID),
		Kind:             savedReportArtifactKind,
		Lifecycle:        "draft",
		Version:          resolveSharedArtifactVersion(normalized.Version, 0),
		ReportID:         reportID,
		Title:            title,
		SourceArtifactID: sourceArtifactID,
		DocumentVersion:  normalized.DocumentVersion,
		Document:         cloneJSON(normalized.ReportDocument),
		ReportSpec:       cloneJSON(normalized.ReportSpec),
		CompileState:     cloneJSON(normalized.CompileState),
		ReportFill:       cloneJSON(normalized.ReportFill),
		ReportPrint:      cloneJSON(normalized.ReportPrint),
		Metadata:         cloneJSON(normalized.Metadata),
		CreatedAt:        s.now().UTC(),
	}
	if err := s.store.CreateSharedArtifact(ctx, artifact); err != nil {
		return nil, err
	}
	return cloneSharedArtifact(artifact), nil
}

// normalizeToolJSONPayload accepts both canonical JSON values and the
// JSON-encoded strings some generic tool clients use for RawMessage fields.
func normalizeToolJSONPayload(raw json.RawMessage) json.RawMessage {
	value := bytes.TrimSpace(raw)
	if len(value) == 0 || value[0] != '"' {
		return cloneJSON(value)
	}
	var encoded string
	if json.Unmarshal(value, &encoded) != nil {
		return cloneJSON(value)
	}
	decoded := bytes.TrimSpace([]byte(encoded))
	if !json.Valid(decoded) {
		return cloneJSON(value)
	}
	return cloneJSON(decoded)
}

func jsonObjectText(raw json.RawMessage, key string) string {
	value := bytes.TrimSpace(raw)
	if len(value) == 0 || !json.Valid(value) {
		return ""
	}
	var object map[string]interface{}
	if json.Unmarshal(value, &object) != nil {
		return ""
	}
	text, _ := object[key].(string)
	return strings.TrimSpace(text)
}

func validateInlineSaveReport(request *SaveReportRequest) error {
	if request == nil {
		return nil
	}
	isReusable := jsonObjectString(request.Metadata, "source", "inline")
	isSnapshot := jsonObjectString(request.Metadata, "source", "inline_snapshot")
	if !isReusable && !isSnapshot {
		return nil
	}
	required := []struct {
		name  string
		value json.RawMessage
	}{
		{name: "reportDocument", value: request.ReportDocument},
		{name: "reportSpec", value: request.ReportSpec},
	}
	for _, item := range required {
		value := bytes.TrimSpace(item.value)
		if len(value) == 0 || !json.Valid(value) {
			return fmt.Errorf("report store: committed inline report requires valid %s", item.name)
		}
	}
	if !jsonObjectString(request.CompileState, "status", "clean") {
		return fmt.Errorf("report store: committed inline report requires clean compileState")
	}
	if isSnapshot {
		return nil
	}
	if !jsonObjectBool(request.Metadata, "reusableDataSources", true) {
		return fmt.Errorf("report store: inline promotion requires resolved reusable workspace data sources")
	}
	if len(bytes.TrimSpace(request.ReportFill)) > 0 || len(bytes.TrimSpace(request.ReportPrint)) > 0 {
		return fmt.Errorf("report store: inline promotion must not persist materialized runtime rows")
	}
	if err := validateInlinePromotionSources(request.ReportDocument, request.ReportSpec, request.Metadata); err != nil {
		return err
	}
	return nil
}

func validateInlinePromotionSources(documentRaw, specRaw, metadataRaw json.RawMessage) error {
	var document struct {
		Datasets []struct {
			ID   string          `json:"id"`
			Rows json.RawMessage `json:"rows"`
		} `json:"datasets"`
	}
	if err := json.Unmarshal(documentRaw, &document); err != nil {
		return fmt.Errorf("report store: committed inline report requires valid reportDocument")
	}
	for _, dataset := range document.Datasets {
		if len(bytes.TrimSpace(dataset.Rows)) > 0 && string(bytes.TrimSpace(dataset.Rows)) != "null" {
			return fmt.Errorf("report store: inline promotion dataset %q contains materialized rows", strings.TrimSpace(dataset.ID))
		}
	}
	var spec struct {
		Datasets []struct {
			ID            string                 `json:"id"`
			DataSourceRef string                 `json:"dataSourceRef"`
			Request       map[string]interface{} `json:"request"`
		} `json:"datasets"`
	}
	if err := json.Unmarshal(specRaw, &spec); err != nil {
		return fmt.Errorf("report store: committed inline report requires valid reportSpec")
	}
	resolvedRefs := jsonObjectStrings(metadataRaw, "resolvedDataSourceRefs")
	resolved := make(map[string]bool, len(resolvedRefs))
	for _, ref := range resolvedRefs {
		resolved[strings.TrimSpace(ref)] = true
	}
	for _, dataset := range spec.Datasets {
		ref := strings.TrimSpace(dataset.DataSourceRef)
		kind, _ := dataset.Request["kind"].(string)
		if ref == "" || strings.HasPrefix(strings.ToLower(ref), "static_json_") || strings.EqualFold(strings.TrimSpace(kind), "staticJson") {
			return fmt.Errorf("report store: inline promotion dataset %q is not backed by a reusable workspace data source", strings.TrimSpace(dataset.ID))
		}
		if !resolved[ref] {
			return fmt.Errorf("report store: inline promotion data source %q was not resolved by the workspace", ref)
		}
	}
	return nil
}

func jsonObjectString(raw json.RawMessage, key, expected string) bool {
	if len(bytes.TrimSpace(raw)) == 0 {
		return false
	}
	var value map[string]interface{}
	if json.Unmarshal(raw, &value) != nil {
		return false
	}
	actual, _ := value[key].(string)
	return strings.EqualFold(strings.TrimSpace(actual), strings.TrimSpace(expected))
}

func jsonObjectBool(raw json.RawMessage, key string, expected bool) bool {
	if len(bytes.TrimSpace(raw)) == 0 {
		return false
	}
	var value map[string]interface{}
	if json.Unmarshal(raw, &value) != nil {
		return false
	}
	actual, ok := value[key].(bool)
	return ok && actual == expected
}

func jsonObjectStrings(raw json.RawMessage, key string) []string {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	var value map[string]interface{}
	if json.Unmarshal(raw, &value) != nil {
		return nil
	}
	items, _ := value[key].([]interface{})
	result := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
			result = append(result, strings.TrimSpace(text))
		}
	}
	return result
}

func (s *Service) saveReportTool(ctx context.Context, in, out interface{}) error {
	input, ok := in.(*SaveReportRequest)
	if !ok {
		return svc.NewInvalidInputError(in)
	}
	output, ok := out.(*SharedArtifact)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	report, err := s.SaveReport(ctx, input)
	if err != nil {
		return err
	}
	*output = *cloneSharedArtifact(report)
	return nil
}

func (s *Service) GetReport(ctx context.Context, input *GetReportInput) (*SharedArtifact, error) {
	if input == nil {
		return nil, fmt.Errorf("report store: get request is required")
	}
	if artifactID := strings.TrimSpace(input.ArtifactID); artifactID != "" {
		artifact, err := s.GetSharedArtifact(ctx, artifactID)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(artifact.Kind) != savedReportArtifactKind {
			return nil, ErrNotFound
		}
		return artifact, nil
	}
	artifactRef := strings.TrimSpace(input.ArtifactRef)
	reportID := strings.TrimSpace(input.ReportID)
	if artifactRef == "" && reportID == "" {
		return nil, fmt.Errorf("report store: artifactId, artifactRef, or reportId is required")
	}
	artifacts, err := s.store.ListSharedArtifacts(ctx)
	if err != nil {
		return nil, err
	}
	for _, artifact := range artifacts {
		if artifact == nil || !isVisibleToActor(ctx, artifact.GetOwnerID()) {
			continue
		}
		if strings.TrimSpace(artifact.Kind) != savedReportArtifactKind {
			continue
		}
		if artifactRef != "" && strings.TrimSpace(artifact.ArtifactRef) == artifactRef {
			return cloneSharedArtifact(artifact), nil
		}
		if reportID != "" && strings.TrimSpace(artifact.ReportID) == reportID {
			return cloneSharedArtifact(artifact), nil
		}
	}
	return nil, ErrNotFound
}

func (s *Service) getReportTool(ctx context.Context, in, out interface{}) error {
	input, ok := in.(*GetReportInput)
	if !ok {
		return svc.NewInvalidInputError(in)
	}
	output, ok := out.(*SharedArtifact)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	report, err := s.GetReport(ctx, input)
	if err != nil {
		return err
	}
	*output = *cloneSharedArtifact(report)
	return nil
}

func (s *Service) ListReports(ctx context.Context, input *ListReportsInput) (*ListReportsResult, error) {
	var normalized ListReportsInput
	if input != nil {
		normalized = *input
	}
	sharedLimit := normalized.Limit
	if strings.TrimSpace(normalized.OrderID) != "" {
		sharedLimit = 0
	}
	result, err := s.ListSharedArtifacts(ctx, &ListSharedArtifactsInput{
		ArtifactRef: strings.TrimSpace(normalized.ArtifactRef),
		ReportID:    strings.TrimSpace(normalized.ReportID),
		Kind:        savedReportArtifactKind,
		Limit:       sharedLimit,
	})
	if err != nil {
		return nil, err
	}
	reports := make([]*ReportSummary, 0, len(result.Artifacts))
	for _, artifact := range result.Artifacts {
		if summary := reportSummary(artifact); summary != nil && reportSummaryMatchesOrder(summary, normalized.OrderID) {
			reports = append(reports, summary)
		}
	}
	totalCount := result.TotalCount
	if strings.TrimSpace(normalized.OrderID) != "" {
		totalCount = len(reports)
	}
	if normalized.Limit > 0 && len(reports) > normalized.Limit {
		reports = reports[:normalized.Limit]
	}
	return &ListReportsResult{
		Reports:    reports,
		TotalCount: totalCount,
	}, nil
}

func (s *Service) listReportsTool(ctx context.Context, in, out interface{}) error {
	input, ok := in.(*ListReportsInput)
	if !ok {
		return svc.NewInvalidInputError(in)
	}
	output, ok := out.(*ListReportsResult)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	result, err := s.ListReports(ctx, input)
	if err != nil {
		return err
	}
	*output = *cloneListReportsResult(result)
	return nil
}

func (s *Service) UpdateReport(ctx context.Context, request *UpdateReportRequest) (*SharedArtifact, error) {
	if request == nil {
		return nil, fmt.Errorf("report store: update request is required")
	}
	current, err := s.GetReport(ctx, &GetReportInput{
		ArtifactID:  request.ArtifactID,
		ArtifactRef: request.ArtifactRef,
		ReportID:    request.ReportID,
	})
	if err != nil {
		return nil, err
	}
	updated := cloneSharedArtifact(current)
	if title := strings.TrimSpace(request.Title); title != "" {
		updated.Title = title
	}
	if request.Version > 0 {
		updated.Version = request.Version
	}
	if request.DocumentVersion > 0 {
		updated.DocumentVersion = request.DocumentVersion
	}
	if len(bytes.TrimSpace(request.ReportDocument)) > 0 {
		updated.Document = cloneJSON(request.ReportDocument)
	}
	if len(bytes.TrimSpace(request.ReportSpec)) > 0 {
		updated.ReportSpec = cloneJSON(request.ReportSpec)
	}
	if len(bytes.TrimSpace(request.CompileState)) > 0 {
		updated.CompileState = cloneJSON(request.CompileState)
	}
	if len(bytes.TrimSpace(request.ReportFill)) > 0 {
		updated.ReportFill = cloneJSON(request.ReportFill)
	}
	if len(bytes.TrimSpace(request.ReportPrint)) > 0 {
		updated.ReportPrint = cloneJSON(request.ReportPrint)
	}
	if len(bytes.TrimSpace(request.Metadata)) > 0 {
		updated.Metadata = cloneJSON(request.Metadata)
	}
	now := s.now().UTC()
	updated.UpdatedAt = &now
	if err := s.store.UpdateSharedArtifact(ctx, updated); err != nil {
		return nil, err
	}
	return cloneSharedArtifact(updated), nil
}

func (s *Service) updateReportTool(ctx context.Context, in, out interface{}) error {
	input, ok := in.(*UpdateReportRequest)
	if !ok {
		return svc.NewInvalidInputError(in)
	}
	output, ok := out.(*SharedArtifact)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	report, err := s.UpdateReport(ctx, input)
	if err != nil {
		return err
	}
	*output = *cloneSharedArtifact(report)
	return nil
}

func (s *Service) DuplicateReport(ctx context.Context, request *DuplicateReportRequest) (*SharedArtifact, error) {
	if request == nil {
		return nil, fmt.Errorf("report store: duplicate request is required")
	}
	current, err := s.GetReport(ctx, &GetReportInput{
		ArtifactID:  request.ArtifactID,
		ArtifactRef: request.ArtifactRef,
		ReportID:    request.ReportID,
	})
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(current.Kind) != savedReportArtifactKind {
		return nil, fmt.Errorf("report store: only user-defined saved reports can be duplicated")
	}
	title := strings.TrimSpace(request.Title)
	if title == "" {
		title = "Copy of " + strings.TrimSpace(current.Title)
	}
	metadata := mergeJSONObject(current.Metadata, map[string]interface{}{
		"duplicatedFromReportId": current.ReportID,
		"duplicatedAt":           s.now().UTC().Format(time.RFC3339Nano),
	})
	return s.SaveReport(ctx, &SaveReportRequest{
		ReportID:        s.newID(),
		Title:           title,
		Version:         1,
		DocumentVersion: current.DocumentVersion,
		ReportDocument:  current.Document,
		ReportSpec:      current.ReportSpec,
		CompileState:    current.CompileState,
		ReportFill:      current.ReportFill,
		ReportPrint:     current.ReportPrint,
		Metadata:        metadata,
	})
}

func (s *Service) duplicateReportTool(ctx context.Context, in, out interface{}) error {
	input, ok := in.(*DuplicateReportRequest)
	if !ok {
		return svc.NewInvalidInputError(in)
	}
	output, ok := out.(*SharedArtifact)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	report, err := s.DuplicateReport(ctx, input)
	if err != nil {
		return err
	}
	*output = *cloneSharedArtifact(report)
	return nil
}

func (s *Service) DeleteReport(ctx context.Context, request *DeleteReportRequest) (*DeleteReportResult, error) {
	if request == nil {
		return nil, fmt.Errorf("report store: delete request is required")
	}
	current, err := s.GetReport(ctx, &GetReportInput{
		ArtifactID:  request.ArtifactID,
		ArtifactRef: request.ArtifactRef,
		ReportID:    request.ReportID,
	})
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(current.Kind) != savedReportArtifactKind {
		return nil, fmt.Errorf("report store: only user-defined saved reports can be deleted")
	}
	if err := s.store.DeleteSharedArtifact(ctx, current.ArtifactID); err != nil {
		return nil, err
	}
	return &DeleteReportResult{
		ArtifactID: current.ArtifactID,
		ReportID:   current.ReportID,
		Deleted:    true,
	}, nil
}

func (s *Service) deleteReportTool(ctx context.Context, in, out interface{}) error {
	input, ok := in.(*DeleteReportRequest)
	if !ok {
		return svc.NewInvalidInputError(in)
	}
	output, ok := out.(*DeleteReportResult)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	result, err := s.DeleteReport(ctx, input)
	if err != nil {
		return err
	}
	*output = *result
	return nil
}

func (s *Service) RecordReportRun(ctx context.Context, request *RecordReportRunRequest) (*SharedArtifact, error) {
	if request == nil {
		return nil, fmt.Errorf("report store: record run request is required")
	}
	current, err := s.GetReport(ctx, &GetReportInput{
		ArtifactID:  request.ArtifactID,
		ArtifactRef: request.ArtifactRef,
		ReportID:    request.ReportID,
	})
	if err != nil {
		return nil, err
	}
	ranAt := request.RanAt
	if ranAt.IsZero() {
		ranAt = s.now().UTC()
	}
	updated := cloneSharedArtifact(current)
	updated.Metadata = mergeJSONObject(updated.Metadata, map[string]interface{}{
		"lastRunAt": ranAt.UTC().Format(time.RFC3339Nano),
	})
	now := s.now().UTC()
	updated.UpdatedAt = &now
	if err := s.store.UpdateSharedArtifact(ctx, updated); err != nil {
		return nil, err
	}
	return cloneSharedArtifact(updated), nil
}

func (s *Service) recordReportRunTool(ctx context.Context, in, out interface{}) error {
	input, ok := in.(*RecordReportRunRequest)
	if !ok {
		return svc.NewInvalidInputError(in)
	}
	output, ok := out.(*SharedArtifact)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	report, err := s.RecordReportRun(ctx, input)
	if err != nil {
		return err
	}
	*output = *cloneSharedArtifact(report)
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

func normalizeShareArtifactRequest(request *ShareArtifactRequest) (*ShareArtifactRequest, error) {
	if request == nil {
		return nil, fmt.Errorf("reporting lifecycle: share request is required")
	}
	next := cloneShareArtifactRequest(request)
	next.ArtifactRef = strings.TrimSpace(next.ArtifactRef)
	next.Lifecycle = normalizeSharedArtifactLifecycle(next.Lifecycle, "draft")
	if next.ArtifactRef == "" {
		return nil, fmt.Errorf("reporting lifecycle: artifactRef is required")
	}
	if next.Version < 0 {
		return nil, fmt.Errorf("reporting lifecycle: version must be >= 0")
	}
	if next.ReportExportRequest != nil {
		if _, err := normalizeSubmitExportRequest(&SubmitExportRequest{ReportExportRequest: next.ReportExportRequest}); err != nil {
			return nil, err
		}
	}
	return next, nil
}

func normalizeTransitionArtifactRequest(request *TransitionArtifactRequest) (*TransitionArtifactRequest, error) {
	if request == nil {
		return nil, fmt.Errorf("reporting lifecycle: transition request is required")
	}
	next := cloneTransitionArtifactRequest(request)
	next.ArtifactRef = strings.TrimSpace(next.ArtifactRef)
	next.From = normalizeSharedArtifactLifecycle(next.From, "")
	next.To = normalizeSharedArtifactLifecycle(next.To, "")
	next.Reason = strings.TrimSpace(next.Reason)
	if next.ArtifactRef == "" {
		return nil, fmt.Errorf("reporting lifecycle: artifactRef is required")
	}
	if next.Version < 0 {
		return nil, fmt.Errorf("reporting lifecycle: version must be >= 0")
	}
	if next.To == "" {
		return nil, fmt.Errorf("reporting lifecycle: transition target is required")
	}
	if next.To != "published" && next.To != "archived" {
		return nil, fmt.Errorf("reporting lifecycle: unsupported transition target %q", strings.TrimSpace(request.To))
	}
	if next.ReportExportRequest != nil {
		if _, err := normalizeSubmitExportRequest(&SubmitExportRequest{ReportExportRequest: next.ReportExportRequest}); err != nil {
			return nil, err
		}
	}
	return next, nil
}

func normalizeSharedArtifactLifecycle(value, fallback string) string {
	normalized := strings.TrimSpace(strings.ToLower(value))
	switch normalized {
	case "draft", "published", "archived":
		return normalized
	case "":
		return strings.TrimSpace(strings.ToLower(fallback))
	default:
		return strings.TrimSpace(strings.ToLower(fallback))
	}
}

func resolveSharedArtifactVersion(primary, secondary int) int {
	if primary > 0 {
		return primary
	}
	if secondary > 0 {
		return secondary
	}
	return 1
}

func normalizeSharedArtifactSourceID(prefix, candidate, reportID, fallback string) string {
	normalizedCandidate := strings.TrimSpace(candidate)
	if normalizedCandidate != "" && strings.HasPrefix(normalizedCandidate, prefix+"_") {
		return normalizedCandidate
	}
	normalizedReportID := strings.TrimSpace(reportID)
	if normalizedReportID != "" {
		return prefix + "_" + normalizedReportID
	}
	if normalizedCandidate != "" {
		return prefix + "_" + normalizedCandidate
	}
	return prefix + "_" + strings.TrimSpace(fallback)
}

func buildSharedArtifactRef(kind, sourceArtifactID string) string {
	normalizedKind := strings.TrimSpace(kind)
	normalizedArtifactID := strings.TrimSpace(sourceArtifactID)
	if normalizedKind == "" || normalizedArtifactID == "" {
		return ""
	}
	return normalizedKind + "://" + normalizedArtifactID
}

func buildOwnerRef(ownerID string) string {
	normalizedOwnerID := strings.TrimSpace(ownerID)
	if normalizedOwnerID == "" {
		return ""
	}
	return "user://" + normalizedOwnerID
}

func (s *Service) findVisibleSharedArtifactByRef(ctx context.Context, artifactRef string) (*SharedArtifact, error) {
	normalizedRef := strings.TrimSpace(artifactRef)
	if normalizedRef == "" {
		return nil, ErrNotFound
	}
	artifacts, err := s.store.ListSharedArtifacts(ctx)
	if err != nil {
		return nil, err
	}
	for _, artifact := range artifacts {
		if artifact == nil || !isVisibleToActor(ctx, artifact.GetOwnerID()) {
			continue
		}
		if strings.TrimSpace(artifact.ArtifactRef) == normalizedRef {
			return cloneSharedArtifact(artifact), nil
		}
	}
	return nil, ErrNotFound
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

func buildAuthContextRef(ctx context.Context) string {
	actorID := strings.TrimSpace(authctx.EffectiveUserID(ctx))
	provider := strings.TrimSpace(iauth.Provider(ctx))
	hasAccessToken := strings.TrimSpace(authctx.MCPAuthToken(ctx, false)) != ""
	hasIDToken := strings.TrimSpace(authctx.MCPAuthToken(ctx, true)) != ""
	if actorID == "" && !hasAccessToken && !hasIDToken {
		return ""
	}
	parts := []string{}
	if actorID != "" {
		parts = append(parts, "actor="+actorID)
	}
	if provider != "" {
		parts = append(parts, "provider="+provider)
	}
	if hasAccessToken {
		parts = append(parts, "access=true")
	}
	if hasIDToken {
		parts = append(parts, "id=true")
	}
	return strings.Join(parts, ";")
}

func parseAuthContextRef(value string) map[string]string {
	result := map[string]string{}
	for _, part := range strings.Split(strings.TrimSpace(value), ";") {
		key, val, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		if key == "" || val == "" {
			continue
		}
		result[key] = val
	}
	return result
}

func (s *Service) ensureRunExportAuthContext(ctx context.Context, job *ExportJob) (context.Context, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if job == nil {
		return ctx, nil
	}
	meta := parseAuthContextRef(job.AuthContextRef)
	actorID := strings.TrimSpace(meta["actor"])
	if actorID == "" {
		actorID = strings.TrimSpace(job.OwnerID)
	}
	if actorID == "" {
		return ctx, nil
	}
	ctx = authctx.InjectUser(ctx, actorID)
	if conversationID := strings.TrimSpace(job.ConversationID); conversationID != "" {
		ctx = runtimerequestctx.WithConversationID(ctx, conversationID)
	}
	provider := strings.TrimSpace(meta["provider"])
	if provider == "" {
		provider = "oauth"
	}
	ctx = iauth.WithProvider(ctx, provider)
	if s.tokenProvider == nil {
		return ctx, nil
	}
	nextCtx, err := s.tokenProvider.EnsureTokens(ctx, tokenctx.Key{
		Subject:  actorID,
		Provider: provider,
	})
	if err != nil {
		return ctx, err
	}
	return nextCtx, nil
}

func isVisibleToActor(ctx context.Context, ownerID string) bool {
	actorID := effectiveActorID(ctx)
	normalizedOwner := strings.TrimSpace(ownerID)
	if normalizedOwner == "" {
		return actorID == ""
	}
	return actorID != "" && actorID == normalizedOwner
}

func isExportJobVisible(ctx context.Context, job *ExportJob) bool {
	if job == nil || !isVisibleToActor(ctx, job.GetOwnerID()) {
		return false
	}
	if strings.TrimSpace(job.ReportRunID) == "" {
		return true
	}
	conversationID := strings.TrimSpace(job.ConversationID)
	currentConversationID := strings.TrimSpace(runtimerequestctx.ConversationIDFromContext(ctx))
	return conversationID != "" &&
		currentConversationID != "" &&
		conversationID == currentConversationID
}

func isCompletedArtifactVisibleToActor(ctx context.Context, artifact *Artifact, job *ExportJob, now time.Time) bool {
	if artifact == nil || job == nil {
		return false
	}
	if !isVisibleToActor(ctx, artifact.GetOwnerID()) || !isExportJobVisible(ctx, job) {
		return false
	}
	if strings.TrimSpace(artifact.JobID) == "" || strings.TrimSpace(job.JobID) == "" {
		return false
	}
	if strings.TrimSpace(artifact.JobID) != strings.TrimSpace(job.JobID) {
		return false
	}
	if job.Status != JobStatusSucceeded {
		return false
	}
	if strings.TrimSpace(job.ArtifactID) == "" || strings.TrimSpace(job.ArtifactID) != strings.TrimSpace(artifact.ArtifactID) {
		return false
	}
	if strings.TrimSpace(job.OwnerID) != strings.TrimSpace(artifact.OwnerID) {
		return false
	}
	if strings.TrimSpace(job.ArtifactRef) != strings.TrimSpace(artifact.ArtifactRef) {
		return false
	}
	if job.Format != artifact.Format {
		return false
	}
	if isArtifactExpired(artifact, job, now) {
		return false
	}
	return true
}

func isArtifactExpired(artifact *Artifact, job *ExportJob, now time.Time) bool {
	if artifact == nil {
		return true
	}
	if artifact.RetentionTTL <= 0 {
		return false
	}
	expiryAnchor := artifact.CreatedAt
	if expiryAnchor.IsZero() && job != nil && job.CompletedAt != nil {
		expiryAnchor = *job.CompletedAt
	}
	if expiryAnchor.IsZero() {
		return true
	}
	return !expiryAnchor.Add(artifact.RetentionTTL).After(now)
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
			Metadata:    cloneJSON(input.ReportExportRequest.Metadata),
		}
	}
	return &SubmitExportRequest{
		ArtifactRef:         strings.TrimSpace(input.ArtifactRef),
		Format:              input.Format,
		Scope:               input.Scope,
		ConversationID:      strings.TrimSpace(input.ConversationID),
		WorkspaceID:         strings.TrimSpace(input.WorkspaceID),
		Source:              cloneExportSource(input.Source),
		ReportSpec:          cloneJSON(input.ReportSpec),
		ReportFill:          cloneJSON(input.ReportFill),
		ReportPrint:         cloneJSON(input.ReportPrint),
		Metadata:            cloneJSON(input.Metadata),
		ReportExportRequest: reportExportRequest,
		ReportRunID:         strings.TrimSpace(input.ReportRunID),
	}
}

func cloneShareArtifactRequest(input *ShareArtifactRequest) *ShareArtifactRequest {
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
			Metadata:    cloneJSON(input.ReportExportRequest.Metadata),
		}
	}
	return &ShareArtifactRequest{
		ArtifactRef:         strings.TrimSpace(input.ArtifactRef),
		Version:             input.Version,
		Lifecycle:           strings.TrimSpace(input.Lifecycle),
		ReportDocument:      cloneJSON(input.ReportDocument),
		ReportExportRequest: reportExportRequest,
		SavedViewOverlay:    cloneJSON(input.SavedViewOverlay),
		Metadata:            cloneJSON(input.Metadata),
	}
}

func cloneTransitionArtifactRequest(input *TransitionArtifactRequest) *TransitionArtifactRequest {
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
			Metadata:    cloneJSON(input.ReportExportRequest.Metadata),
		}
	}
	return &TransitionArtifactRequest{
		ArtifactRef:         strings.TrimSpace(input.ArtifactRef),
		From:                strings.TrimSpace(input.From),
		To:                  strings.TrimSpace(input.To),
		Reason:              strings.TrimSpace(input.Reason),
		Version:             input.Version,
		ReportDocument:      cloneJSON(input.ReportDocument),
		ReportExportRequest: reportExportRequest,
		Metadata:            cloneJSON(input.Metadata),
	}
}

func cloneJob(input *ExportJob) *ExportJob {
	if input == nil {
		return nil
	}
	out := &ExportJob{
		JobID:             strings.TrimSpace(input.JobID),
		ArtifactRef:       strings.TrimSpace(input.ArtifactRef),
		OwnerID:           strings.TrimSpace(input.OwnerID),
		ConversationID:    strings.TrimSpace(input.ConversationID),
		WorkspaceID:       strings.TrimSpace(input.WorkspaceID),
		AuthContextRef:    strings.TrimSpace(input.AuthContextRef),
		Format:            input.Format,
		Scope:             input.Scope,
		Status:            input.Status,
		ReportRunID:       strings.TrimSpace(input.ReportRunID),
		ReportRunRevision: input.ReportRunRevision,
		ExportRequestID:   strings.TrimSpace(input.ExportRequestID),
		ReportSpec:        cloneJSON(input.ReportSpec),
		ReportFill:        cloneJSON(input.ReportFill),
		ReportPrint:       cloneJSON(input.ReportPrint),
		Metadata:          cloneJSON(input.Metadata),
		ArtifactID:        strings.TrimSpace(input.ArtifactID),
		Error:             strings.TrimSpace(input.Error),
		Diagnostics:       cloneDiagnostics(input.Diagnostics),
		SubmittedAt:       input.SubmittedAt,
		RetentionTTL:      input.RetentionTTL,
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

func exportJobStatus(input *ExportJob) *ExportJobStatus {
	if input == nil {
		return nil
	}
	out := &ExportJobStatus{
		JobID:          input.JobID,
		ArtifactRef:    input.ArtifactRef,
		OwnerID:        input.OwnerID,
		ConversationID: input.ConversationID,
		WorkspaceID:    input.WorkspaceID,
		ReportRunID:    input.ReportRunID,
		Format:         input.Format,
		Scope:          input.Scope,
		Status:         input.Status,
		ArtifactID:     input.ArtifactID,
		Error:          input.Error,
		Diagnostics:    cloneDiagnostics(input.Diagnostics),
		SubmittedAt:    input.SubmittedAt,
		RetentionTTL:   input.RetentionTTL,
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

func cloneRunQueuedExportsResult(input *RunQueuedExportsResult) *RunQueuedExportsResult {
	if input == nil {
		return nil
	}
	result := &RunQueuedExportsResult{
		Jobs:           make([]*ExportJob, 0, len(input.Jobs)),
		ProcessedCount: input.ProcessedCount,
		SucceededCount: input.SucceededCount,
		FailedCount:    input.FailedCount,
	}
	for _, job := range input.Jobs {
		if cloned := cloneJob(job); cloned != nil {
			result.Jobs = append(result.Jobs, cloned)
		}
	}
	return result
}

func cloneListExportJobsResult(input *ListExportJobsResult) *ListExportJobsResult {
	if input == nil {
		return nil
	}
	result := &ListExportJobsResult{
		Jobs:       make([]*ExportJob, 0, len(input.Jobs)),
		TotalCount: input.TotalCount,
	}
	for _, job := range input.Jobs {
		if cloned := cloneJob(job); cloned != nil {
			result.Jobs = append(result.Jobs, cloned)
		}
	}
	return result
}

func cloneListExportArtifactsResult(input *ListExportArtifactsResult) *ListExportArtifactsResult {
	if input == nil {
		return nil
	}
	result := &ListExportArtifactsResult{
		Artifacts:  make([]*Artifact, 0, len(input.Artifacts)),
		TotalCount: input.TotalCount,
	}
	for _, artifact := range input.Artifacts {
		if cloned := cloneArtifact(artifact); cloned != nil {
			result.Artifacts = append(result.Artifacts, cloned)
		}
	}
	return result
}

func cloneListSharedArtifactsResult(input *ListSharedArtifactsResult) *ListSharedArtifactsResult {
	if input == nil {
		return nil
	}
	result := &ListSharedArtifactsResult{
		Artifacts:  make([]*SharedArtifact, 0, len(input.Artifacts)),
		TotalCount: input.TotalCount,
	}
	for _, artifact := range input.Artifacts {
		if cloned := cloneSharedArtifact(artifact); cloned != nil {
			result.Artifacts = append(result.Artifacts, cloned)
		}
	}
	return result
}

func cloneListReportsResult(input *ListReportsResult) *ListReportsResult {
	if input == nil {
		return nil
	}
	result := &ListReportsResult{
		Reports:    make([]*ReportSummary, 0, len(input.Reports)),
		TotalCount: input.TotalCount,
	}
	for _, report := range input.Reports {
		if cloned := cloneReportSummary(report); cloned != nil {
			result.Reports = append(result.Reports, cloned)
		}
	}
	return result
}

func reportSummary(input *SharedArtifact) *ReportSummary {
	if input == nil {
		return nil
	}
	catalog := deriveReportCatalogMetadata(input)
	return &ReportSummary{
		ArtifactID:       strings.TrimSpace(input.ArtifactID),
		ArtifactRef:      strings.TrimSpace(input.ArtifactRef),
		ReportID:         strings.TrimSpace(input.ReportID),
		Title:            strings.TrimSpace(input.Title),
		OwnerID:          strings.TrimSpace(input.OwnerID),
		ReportType:       catalog.ReportType,
		BuilderRef:       catalog.BuilderRef,
		OrderIDs:         catalog.OrderIDs,
		DefaultFrom:      catalog.DefaultFrom,
		DefaultTo:        catalog.DefaultTo,
		LastRunAt:        catalog.LastRunAt,
		Lifecycle:        strings.TrimSpace(input.Lifecycle),
		Version:          input.Version,
		DocumentVersion:  input.DocumentVersion,
		SourceArtifactID: strings.TrimSpace(input.SourceArtifactID),
		CreatedAt:        input.CreatedAt,
		UpdatedAt:        cloneTime(input.UpdatedAt),
	}
}

func cloneReportSummary(input *ReportSummary) *ReportSummary {
	if input == nil {
		return nil
	}
	result := *input
	result.OrderIDs = append([]string{}, input.OrderIDs...)
	result.LastRunAt = cloneTime(input.LastRunAt)
	result.UpdatedAt = cloneTime(input.UpdatedAt)
	return &result
}

type reportCatalogMetadata struct {
	ReportType  string
	BuilderRef  string
	OrderIDs    []string
	DefaultFrom string
	DefaultTo   string
	LastRunAt   *time.Time
}

func deriveReportCatalogMetadata(input *SharedArtifact) reportCatalogMetadata {
	result := reportCatalogMetadata{ReportType: "Saved report"}
	values := make([]interface{}, 0, 3)
	for _, raw := range []json.RawMessage{input.Metadata, input.Document, input.ReportSpec} {
		var value interface{}
		if len(bytes.TrimSpace(raw)) > 0 && json.Unmarshal(raw, &value) == nil {
			values = append(values, value)
		}
	}
	orderIDs := map[string]struct{}{}
	for _, value := range values {
		collectReportCatalogValues(value, &result, orderIDs)
	}
	for value := range orderIDs {
		result.OrderIDs = append(result.OrderIDs, value)
	}
	sort.Strings(result.OrderIDs)
	if result.BuilderRef != "" {
		result.ReportType = "Performance report"
	}
	return result
}

func collectReportCatalogValues(value interface{}, result *reportCatalogMetadata, orderIDs map[string]struct{}) {
	switch actual := value.(type) {
	case map[string]interface{}:
		for key, child := range actual {
			normalizedKey := strings.ToLower(strings.TrimSpace(key))
			switch normalizedKey {
			case "builderref", "reportbuilderref", "workspaceid":
				if result.BuilderRef == "" {
					result.BuilderRef = strings.TrimSpace(fmt.Sprint(child))
				}
			case "reporttype":
				if text := strings.TrimSpace(fmt.Sprint(child)); text != "" {
					result.ReportType = text
				}
			case "orderid", "adorderid", "orderids", "adorderids":
				collectCatalogIDs(child, orderIDs)
			case "lastrunat":
				if result.LastRunAt == nil {
					if parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(fmt.Sprint(child))); err == nil {
						parsed = parsed.UTC()
						result.LastRunAt = &parsed
					}
				}
			case "from":
				if result.DefaultFrom == "" && looksLikeISODate(child) {
					result.DefaultFrom = strings.TrimSpace(fmt.Sprint(child))
				}
			case "to":
				if result.DefaultTo == "" && looksLikeISODate(child) {
					result.DefaultTo = strings.TrimSpace(fmt.Sprint(child))
				}
			}
			collectReportCatalogValues(child, result, orderIDs)
		}
	case []interface{}:
		for _, child := range actual {
			collectReportCatalogValues(child, result, orderIDs)
		}
	}
}

func collectCatalogIDs(value interface{}, target map[string]struct{}) {
	switch actual := value.(type) {
	case []interface{}:
		for _, child := range actual {
			collectCatalogIDs(child, target)
		}
	case float64:
		if actual == float64(int64(actual)) {
			target[strconv.FormatInt(int64(actual), 10)] = struct{}{}
		}
	case json.Number:
		if text := strings.TrimSpace(actual.String()); text != "" {
			target[text] = struct{}{}
		}
	default:
		text := strings.TrimSpace(fmt.Sprint(actual))
		if text != "" && text != "<nil>" {
			target[text] = struct{}{}
		}
	}
}

func looksLikeISODate(value interface{}) bool {
	text := strings.TrimSpace(fmt.Sprint(value))
	if len(text) < len("2006-01-02") {
		return false
	}
	_, err := time.Parse("2006-01-02", text[:10])
	return err == nil
}

func reportSummaryMatchesOrder(summary *ReportSummary, orderID string) bool {
	normalized := strings.TrimSpace(orderID)
	if normalized == "" {
		return true
	}
	for _, candidate := range summary.OrderIDs {
		if strings.TrimSpace(candidate) == normalized {
			return true
		}
	}
	return false
}

func mergeJSONObject(raw json.RawMessage, additions map[string]interface{}) json.RawMessage {
	result := map[string]interface{}{}
	if len(bytes.TrimSpace(raw)) > 0 {
		_ = json.Unmarshal(raw, &result)
	}
	for key, value := range additions {
		result[key] = value
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return cloneJSON(raw)
	}
	return encoded
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
		SourceURL:    strings.TrimSpace(input.SourceURL),
		CreatedAt:    input.CreatedAt,
		RetentionTTL: input.RetentionTTL,
	}
	if len(input.Data) > 0 {
		out.Data = append([]byte{}, input.Data...)
	}
	return out
}

func cloneSharedArtifact(input *SharedArtifact) *SharedArtifact {
	if input == nil {
		return nil
	}
	out := &SharedArtifact{
		ArtifactID:       strings.TrimSpace(input.ArtifactID),
		ArtifactRef:      strings.TrimSpace(input.ArtifactRef),
		OwnerID:          strings.TrimSpace(input.OwnerID),
		OwnerRef:         strings.TrimSpace(input.OwnerRef),
		Kind:             strings.TrimSpace(input.Kind),
		Lifecycle:        strings.TrimSpace(input.Lifecycle),
		Version:          input.Version,
		ReportID:         strings.TrimSpace(input.ReportID),
		Title:            strings.TrimSpace(input.Title),
		SourceArtifactID: strings.TrimSpace(input.SourceArtifactID),
		BaseArtifactRef:  strings.TrimSpace(input.BaseArtifactRef),
		PolicyRef:        strings.TrimSpace(input.PolicyRef),
		DocumentVersion:  input.DocumentVersion,
		Document:         cloneJSON(input.Document),
		ReportSpec:       cloneJSON(input.ReportSpec),
		CompileState:     cloneJSON(input.CompileState),
		ReportFill:       cloneJSON(input.ReportFill),
		ReportPrint:      cloneJSON(input.ReportPrint),
		SavedViewOverlay: cloneJSON(input.SavedViewOverlay),
		Metadata:         cloneJSON(input.Metadata),
		CreatedAt:        input.CreatedAt,
	}
	if input.UpdatedAt != nil {
		updatedAt := *input.UpdatedAt
		out.UpdatedAt = &updatedAt
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
		Version:     input.Version,
		JobID:       strings.TrimSpace(input.JobID),
		ArtifactID:  strings.TrimSpace(input.ArtifactID),
		ActorID:     strings.TrimSpace(input.ActorID),
		ActorRef:    strings.TrimSpace(input.ActorRef),
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

func (a *SharedArtifact) GetOwnerID() string {
	if a == nil {
		return ""
	}
	return a.OwnerID
}
