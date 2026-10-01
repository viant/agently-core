package reporting

import (
	"context"
	"errors"
	reportartifactmodel "github.com/viant/agently-core/model/reportartifact"
	reportcontextmodel "github.com/viant/agently-core/model/reportcontext"
	reportjobmodel "github.com/viant/agently-core/model/reportjob"
	reportrunmodel "github.com/viant/agently-core/model/reportrun"
	reportshareartifactmodel "github.com/viant/agently-core/model/reportshareartifact"
	"strings"
	"time"
)

var (
	// ErrAlreadyExists indicates a storage collision on a reporting job or artifact ID.
	ErrAlreadyExists = errors.New("reporting store: already exists")
	// ErrNotFound hides records outside the authenticated owner scope.
	ErrNotFound = errors.New("reporting store: not found")
	// ErrCASMismatch indicates that an expected revision is stale.
	ErrCASMismatch = errors.New("reporting store: revision mismatch")
	// ErrImmutable indicates an attempted mutation of a completed report
	// snapshot outside the one supported adoption transaction.
	ErrImmutable = errors.New("reporting store: completed snapshot is immutable")
	// ErrConflict indicates an idempotency replay whose immutable request
	// identity does not match the already persisted job.
	ErrConflict = errors.New("reporting store: conflict")
	// ErrSchemaRequired indicates that the additive manual T2 schema has not
	// been applied. Runtime store initialization never applies it.
	ErrSchemaRequired = errors.New("reporting store: additive T2 schema is required")
	// ErrInvalidTransition indicates a stale or invalid export-job lifecycle
	// transition.
	ErrInvalidTransition = errors.New("reporting store: invalid job transition")
)

// Client persists reporting export jobs and artifacts.
type Client interface {
	CreateJob(ctx context.Context, job *reportjobmodel.Record) error
	GetJob(ctx context.Context, jobID string) (*reportjobmodel.Record, error)
	ListJobs(ctx context.Context) ([]*reportjobmodel.Record, error)
	UpdateJob(ctx context.Context, job *reportjobmodel.Record) error
	PutArtifact(ctx context.Context, artifact *reportartifactmodel.Record) error
	GetArtifact(ctx context.Context, artifactID string) (*reportartifactmodel.Record, error)
	ListArtifacts(ctx context.Context) ([]*reportartifactmodel.Record, error)
	CreateSharedArtifact(ctx context.Context, artifact *reportshareartifactmodel.Record) error
	GetSharedArtifact(ctx context.Context, artifactID string) (*reportshareartifactmodel.Record, error)
	ListSharedArtifacts(ctx context.Context) ([]*reportshareartifactmodel.Record, error)
	UpdateSharedArtifact(ctx context.Context, artifact *reportshareartifactmodel.Record) error
	DeleteSharedArtifact(ctx context.Context, artifactID string) error
}

// RunClient persists durable browser report runs and their active conversation
// pointers. Implementations must scope reads and writes to the authenticated
// owner in ctx.
type RunClient interface {
	CreateReportRun(ctx context.Context, run *reportrunmodel.Record) error
	GetReportRun(ctx context.Context, reportRunID string) (*reportrunmodel.Record, error)
	GetReportRunByRequestID(ctx context.Context, uiRunRequestID string) (*reportrunmodel.Record, error)
	UpdateReportRunCAS(ctx context.Context, run *reportrunmodel.Record, expectedRevision int64) error
	GetConversationReportContext(ctx context.Context, conversationID string) (*reportcontextmodel.Record, error)
	PutConversationReportContextCAS(ctx context.Context, record *reportcontextmodel.Record, expectedRevision int64) error
	AdoptReportRunAndContextCAS(ctx context.Context, run *reportrunmodel.Record, expectedRunRevision int64, record *reportcontextmodel.Record, expectedContextRevision int64) error
}

// RunExportClient owns the T2 transactional/recoverable boundaries. The
// candidate job contains trusted identity and export options only;
// SubmitJobFromRun must copy the snapshot from the exact persisted run.
type RunExportClient interface {
	SubmitJobFromRun(ctx context.Context, candidate *reportjobmodel.Record) (job *reportjobmodel.Record, replay bool, err error)
	ClaimJob(ctx context.Context, jobID string, startedAt time.Time) (*reportjobmodel.Record, error)
	CompleteJobWithArtifact(ctx context.Context, jobID string, artifact *reportartifactmodel.Record, diagnostics []byte, completedAt time.Time, retentionTTL time.Duration) (*reportjobmodel.Record, error)
	FailJob(ctx context.Context, jobID, errorText string, diagnostics []byte, completedAt time.Time) (*reportjobmodel.Record, error)
	ReconcileRunningJobs(ctx context.Context, staleBefore, reconciledAt time.Time, errorText string) ([]*reportjobmodel.Record, error)
}

// ValidateRunExportCandidate limits the trusted submit boundary to exact run
// identity plus the currently supported exporter choices. The run revision and
// snapshots are deliberately absent here: stores derive and copy them.
func ValidateRunExportCandidate(candidate *reportjobmodel.Record) error {
	if candidate == nil {
		return ErrNotFound
	}
	reportRunID := strings.TrimSpace(candidate.ReportRunID)
	exportRequestID := strings.TrimSpace(candidate.ExportRequestID)
	if reportRunID == "" ||
		strings.TrimSpace(candidate.ConversationID) == "" ||
		exportRequestID == "" ||
		len(exportRequestID) > 128 ||
		strings.TrimSpace(candidate.ArtifactRef) != "report-run://"+reportRunID ||
		strings.TrimSpace(candidate.Format) != "pdf" ||
		strings.TrimSpace(candidate.Scope) != "draft" ||
		strings.TrimSpace(candidate.Status) != "queued" ||
		candidate.ReportRunRevision != 0 ||
		strings.TrimSpace(candidate.WorkspaceID) != "" ||
		len(candidate.Metadata) != 0 ||
		strings.TrimSpace(candidate.ArtifactID) != "" ||
		strings.TrimSpace(candidate.Error) != "" ||
		len(candidate.Diagnostics) != 0 ||
		candidate.StartedAt != nil ||
		candidate.CompletedAt != nil ||
		candidate.RetentionTTL != 0 {
		return ErrConflict
	}
	return nil
}

// HasRunExportLink reports whether any nullable T2 link field is populated.
// Such jobs must be created and transitioned through RunExportClient.
func HasRunExportLink(job *reportjobmodel.Record) bool {
	return job != nil &&
		(strings.TrimSpace(job.ReportRunID) != "" ||
			job.ReportRunRevision != 0 ||
			strings.TrimSpace(job.ExportRequestID) != "")
}
