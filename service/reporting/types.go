// Package reporting defines the agently-core reporting runtime boundary for
// canonical compile/fill/print-backed export operations.
package reporting

import (
	"context"
	"encoding/json"
	"time"
)

// ExportFormat identifies the requested artifact type.
type ExportFormat string

const (
	ExportFormatPDF  ExportFormat = "pdf"
	ExportFormatCSV  ExportFormat = "csv"
	ExportFormatXLSX ExportFormat = "xlsx"
)

// ExportScope identifies the export source lifecycle.
type ExportScope string

const (
	ExportScopeDraft             ExportScope = "draft"
	ExportScopeSavedPayload      ExportScope = "saved_payload"
	ExportScopeSavedView         ExportScope = "saved_view"
	ExportScopePublishedSnapshot ExportScope = "published_snapshot"
)

// JobStatus identifies the async export lifecycle.
type JobStatus string

const (
	JobStatusQueued    JobStatus = "queued"
	JobStatusRunning   JobStatus = "running"
	JobStatusSucceeded JobStatus = "succeeded"
	JobStatusFailed    JobStatus = "failed"
)

// Diagnostic is the structured backend diagnostic shape used across compile
// and export operations.
type Diagnostic struct {
	Code         string `json:"code,omitempty"`
	Severity     string `json:"severity,omitempty"`
	Path         string `json:"path,omitempty"`
	Message      string `json:"message,omitempty"`
	SuggestedFix string `json:"suggestedFix,omitempty"`
}

const (
	SourceKindReportSpec = "reportSpec"
)

// CompileRequest carries an authored artifact into the backend compile seam.
type CompileRequest struct {
	ArtifactRef string          `json:"artifactRef,omitempty"`
	SourceKind  string          `json:"sourceKind,omitempty"`
	Document    json.RawMessage `json:"document,omitempty"`
}

// CompileResult is the canonical compile response.
type CompileResult struct {
	ArtifactRef string          `json:"artifactRef,omitempty"`
	ReportSpec  json.RawMessage `json:"reportSpec,omitempty"`
	Diagnostics []Diagnostic    `json:"diagnostics,omitempty"`
	CompiledAt  time.Time       `json:"compiledAt,omitempty"`
}

// RenderRequest is the worker-facing canonical export payload handed to the
// backend exporter boundary.
type RenderRequest struct {
	JobID          string          `json:"jobId,omitempty"`
	ArtifactRef    string          `json:"artifactRef,omitempty"`
	OwnerID        string          `json:"ownerId,omitempty"`
	ConversationID string          `json:"conversationId,omitempty"`
	WorkspaceID    string          `json:"workspaceId,omitempty"`
	AuthContextRef string          `json:"authContextRef,omitempty"`
	Format         ExportFormat    `json:"format,omitempty"`
	Scope          ExportScope     `json:"scope,omitempty"`
	ReportSpec     json.RawMessage `json:"reportSpec,omitempty"`
	ReportFill     json.RawMessage `json:"reportFill,omitempty"`
	ReportPrint    json.RawMessage `json:"reportPrint,omitempty"`
	Metadata       json.RawMessage `json:"metadata,omitempty"`
}

// RenderResult is the exporter output consumed by agently-core artifact
// persistence.
type RenderResult struct {
	ContentType  string        `json:"contentType,omitempty"`
	Data         []byte        `json:"data,omitempty"`
	Diagnostics  []Diagnostic  `json:"diagnostics,omitempty"`
	RetentionTTL time.Duration `json:"retentionTtl,omitempty"`
}

// SubmitExportRequest queues an export job against canonical reporting models.
type SubmitExportRequest struct {
	ArtifactRef         string               `json:"artifactRef,omitempty"`
	Format              ExportFormat         `json:"format,omitempty"`
	Scope               ExportScope          `json:"scope,omitempty"`
	ConversationID      string               `json:"conversationId,omitempty"`
	WorkspaceID         string               `json:"workspaceId,omitempty"`
	ReportSpec          json.RawMessage      `json:"reportSpec,omitempty"`
	ReportFill          json.RawMessage      `json:"reportFill,omitempty"`
	ReportPrint         json.RawMessage      `json:"reportPrint,omitempty"`
	Metadata            json.RawMessage      `json:"metadata,omitempty"`
	ReportExportRequest *ReportExportRequest `json:"reportExportRequest,omitempty"`
}

// RecordAuditEventInput carries a UI-originated reporting audit event through
// the reporting service boundary.
type RecordAuditEventInput struct {
	Event *AuditEvent `json:"event,omitempty"`
}

// ShareArtifactRequest creates or returns a shared reporting artifact through
// the reporting persistence boundary.
type ShareArtifactRequest struct {
	ArtifactRef         string               `json:"artifactRef,omitempty"`
	Version             int                  `json:"version,omitempty"`
	Lifecycle           string               `json:"lifecycle,omitempty"`
	ReportDocument      json.RawMessage      `json:"reportDocument,omitempty"`
	ReportExportRequest *ReportExportRequest `json:"reportExportRequest,omitempty"`
	SavedViewOverlay    json.RawMessage      `json:"savedViewOverlay,omitempty"`
	Metadata            json.RawMessage      `json:"metadata,omitempty"`
}

// TransitionArtifactRequest mutates or materializes a lifecycle transition
// through the reporting persistence boundary.
type TransitionArtifactRequest struct {
	ArtifactRef         string               `json:"artifactRef,omitempty"`
	From                string               `json:"from,omitempty"`
	To                  string               `json:"to,omitempty"`
	Reason              string               `json:"reason,omitempty"`
	Version             int                  `json:"version,omitempty"`
	ReportDocument      json.RawMessage      `json:"reportDocument,omitempty"`
	ReportExportRequest *ReportExportRequest `json:"reportExportRequest,omitempty"`
	Metadata            json.RawMessage      `json:"metadata,omitempty"`
}

// ReportExportRequest is the canonical Forge export handoff envelope accepted
// by agently-core reporting export submission.
type ReportExportRequest struct {
	Version     int                `json:"version,omitempty"`
	Kind        string             `json:"kind,omitempty"`
	Target      ReportExportTarget `json:"target,omitempty"`
	Source      ReportExportSource `json:"source,omitempty"`
	ReportSpec  json.RawMessage    `json:"reportSpec,omitempty"`
	ReportFill  json.RawMessage    `json:"reportFill,omitempty"`
	ReportPrint json.RawMessage    `json:"reportPrint,omitempty"`
	Metadata    json.RawMessage    `json:"metadata,omitempty"`
}

type ReportExportTarget struct {
	Format ExportFormat `json:"format,omitempty"`
}

type ReportExportSource struct {
	From             string `json:"from,omitempty"`
	ArtifactKind     string `json:"artifactKind,omitempty"`
	ArtifactRef      string `json:"artifactRef,omitempty"`
	Title            string `json:"title,omitempty"`
	ReportID         string `json:"reportId,omitempty"`
	PayloadID        string `json:"payloadId,omitempty"`
	SourceArtifactID string `json:"sourceArtifactId,omitempty"`
	DocumentVersion  int    `json:"documentVersion,omitempty"`
}

// CompleteExportRequest records a successful export artifact.
type CompleteExportRequest struct {
	JobID        string        `json:"jobId,omitempty"`
	ContentType  string        `json:"contentType,omitempty"`
	Data         []byte        `json:"data,omitempty"`
	Diagnostics  []Diagnostic  `json:"diagnostics,omitempty"`
	RetentionTTL time.Duration `json:"retentionTtl,omitempty"`
}

// FailExportRequest records a failed export result.
type FailExportRequest struct {
	JobID       string       `json:"jobId,omitempty"`
	Error       string       `json:"error,omitempty"`
	Diagnostics []Diagnostic `json:"diagnostics,omitempty"`
}

// ExportJob is the persisted async export job state.
type ExportJob struct {
	JobID          string          `json:"jobId,omitempty"`
	ArtifactRef    string          `json:"artifactRef,omitempty"`
	OwnerID        string          `json:"ownerId,omitempty"`
	ConversationID string          `json:"conversationId,omitempty"`
	WorkspaceID    string          `json:"workspaceId,omitempty"`
	AuthContextRef string          `json:"authContextRef,omitempty"`
	Format         ExportFormat    `json:"format,omitempty"`
	Scope          ExportScope     `json:"scope,omitempty"`
	Status         JobStatus       `json:"status,omitempty"`
	ReportSpec     json.RawMessage `json:"reportSpec,omitempty"`
	ReportFill     json.RawMessage `json:"reportFill,omitempty"`
	ReportPrint    json.RawMessage `json:"reportPrint,omitempty"`
	Metadata       json.RawMessage `json:"metadata,omitempty"`
	ArtifactID     string          `json:"artifactId,omitempty"`
	Error          string          `json:"error,omitempty"`
	Diagnostics    []Diagnostic    `json:"diagnostics,omitempty"`
	SubmittedAt    time.Time       `json:"submittedAt,omitempty"`
	StartedAt      *time.Time      `json:"startedAt,omitempty"`
	CompletedAt    *time.Time      `json:"completedAt,omitempty"`
	RetentionTTL   time.Duration   `json:"retentionTtl,omitempty"`
}

// Artifact is the downloadable export payload persisted by agently-core.
type Artifact struct {
	ArtifactID   string        `json:"artifactId,omitempty"`
	JobID        string        `json:"jobId,omitempty"`
	ArtifactRef  string        `json:"artifactRef,omitempty"`
	OwnerID      string        `json:"ownerId,omitempty"`
	Format       ExportFormat  `json:"format,omitempty"`
	ContentType  string        `json:"contentType,omitempty"`
	Data         []byte        `json:"data,omitempty"`
	CreatedAt    time.Time     `json:"createdAt,omitempty"`
	RetentionTTL time.Duration `json:"retentionTtl,omitempty"`
}

// SharedArtifact is the persisted saved-view / published-snapshot shell owned
// by agently-core. Payload fields remain opaque JSON at this boundary.
type SharedArtifact struct {
	ArtifactID       string          `json:"artifactId,omitempty"`
	ArtifactRef      string          `json:"artifactRef,omitempty"`
	OwnerID          string          `json:"ownerId,omitempty"`
	OwnerRef         string          `json:"ownerRef,omitempty"`
	Kind             string          `json:"kind,omitempty"`
	Lifecycle        string          `json:"lifecycle,omitempty"`
	Version          int             `json:"version,omitempty"`
	ReportID         string          `json:"reportId,omitempty"`
	Title            string          `json:"title,omitempty"`
	SourceArtifactID string          `json:"sourceArtifactId,omitempty"`
	BaseArtifactRef  string          `json:"baseArtifactRef,omitempty"`
	PolicyRef        string          `json:"policyRef,omitempty"`
	DocumentVersion  int             `json:"documentVersion,omitempty"`
	Document         json.RawMessage `json:"document,omitempty"`
	ReportSpec       json.RawMessage `json:"reportSpec,omitempty"`
	CompileState     json.RawMessage `json:"compileState,omitempty"`
	ReportFill       json.RawMessage `json:"reportFill,omitempty"`
	ReportPrint      json.RawMessage `json:"reportPrint,omitempty"`
	SavedViewOverlay json.RawMessage `json:"savedViewOverlay,omitempty"`
	Metadata         json.RawMessage `json:"metadata,omitempty"`
	CreatedAt        time.Time       `json:"createdAt,omitempty"`
	UpdatedAt        *time.Time      `json:"updatedAt,omitempty"`
}

type SaveReportRequest struct {
	ArtifactRef     string          `json:"artifactRef,omitempty"`
	ReportID        string          `json:"reportId,omitempty"`
	Title           string          `json:"title,omitempty"`
	Version         int             `json:"version,omitempty"`
	DocumentVersion int             `json:"documentVersion,omitempty"`
	ReportDocument  json.RawMessage `json:"reportDocument,omitempty"`
	ReportSpec      json.RawMessage `json:"reportSpec,omitempty"`
	CompileState    json.RawMessage `json:"compileState,omitempty"`
	ReportFill      json.RawMessage `json:"reportFill,omitempty"`
	ReportPrint     json.RawMessage `json:"reportPrint,omitempty"`
	Metadata        json.RawMessage `json:"metadata,omitempty"`
}

type GetReportInput struct {
	ArtifactID  string `json:"artifactId,omitempty"`
	ArtifactRef string `json:"artifactRef,omitempty"`
	ReportID    string `json:"reportId,omitempty"`
}

type ListReportsInput struct {
	ArtifactRef string `json:"artifactRef,omitempty"`
	ReportID    string `json:"reportId,omitempty"`
	Limit       int    `json:"limit,omitempty"`
}

type ListReportsResult struct {
	Reports    []*SharedArtifact `json:"reports,omitempty"`
	TotalCount int               `json:"totalCount,omitempty"`
}

type UpdateReportRequest struct {
	ArtifactID      string          `json:"artifactId,omitempty"`
	ArtifactRef     string          `json:"artifactRef,omitempty"`
	ReportID        string          `json:"reportId,omitempty"`
	Title           string          `json:"title,omitempty"`
	Version         int             `json:"version,omitempty"`
	DocumentVersion int             `json:"documentVersion,omitempty"`
	ReportDocument  json.RawMessage `json:"reportDocument,omitempty"`
	ReportSpec      json.RawMessage `json:"reportSpec,omitempty"`
	CompileState    json.RawMessage `json:"compileState,omitempty"`
	ReportFill      json.RawMessage `json:"reportFill,omitempty"`
	ReportPrint     json.RawMessage `json:"reportPrint,omitempty"`
	Metadata        json.RawMessage `json:"metadata,omitempty"`
}

// Compiler lowers authored artifacts into canonical ReportSpec payloads.
type Compiler interface {
	Compile(ctx context.Context, request *CompileRequest) (*CompileResult, error)
}

// Exporter turns canonical report artifacts into a persisted downloadable
// artifact without re-interpreting authoring semantics.
type Exporter interface {
	Export(ctx context.Context, request *RenderRequest) (*RenderResult, error)
}

// Store persists export jobs and artifacts.
type Store interface {
	CreateJob(ctx context.Context, job *ExportJob) error
	GetJob(ctx context.Context, jobID string) (*ExportJob, error)
	ListJobs(ctx context.Context) ([]*ExportJob, error)
	UpdateJob(ctx context.Context, job *ExportJob) error
	PutArtifact(ctx context.Context, artifact *Artifact) error
	GetArtifact(ctx context.Context, artifactID string) (*Artifact, error)
	ListArtifacts(ctx context.Context) ([]*Artifact, error)
	CreateSharedArtifact(ctx context.Context, artifact *SharedArtifact) error
	GetSharedArtifact(ctx context.Context, artifactID string) (*SharedArtifact, error)
	ListSharedArtifacts(ctx context.Context) ([]*SharedArtifact, error)
	UpdateSharedArtifact(ctx context.Context, artifact *SharedArtifact) error
}

// AuditEvent is the generic reporting audit payload emitted by the service.
type AuditEvent struct {
	EventType   string                 `json:"eventType,omitempty"`
	ArtifactRef string                 `json:"artifactRef,omitempty"`
	Version     int                    `json:"version,omitempty"`
	JobID       string                 `json:"jobId,omitempty"`
	ArtifactID  string                 `json:"artifactId,omitempty"`
	ActorID     string                 `json:"actorId,omitempty"`
	ActorRef    string                 `json:"actorRef,omitempty"`
	OccurredAt  time.Time              `json:"occurredAt,omitempty"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
}

// AuditSink records reporting lifecycle events.
type AuditSink interface {
	Record(ctx context.Context, event *AuditEvent) error
}
