package reporting

import (
	"context"
	"errors"
	"strings"
	"time"

	reportartifact "github.com/viant/agently-core/pkg/agently/reportartifact"
	reportcontext "github.com/viant/agently-core/pkg/agently/reportcontext"
	reportjob "github.com/viant/agently-core/pkg/agently/reportjob"
	reportrun "github.com/viant/agently-core/pkg/agently/reportrun"
	reportshareartifact "github.com/viant/agently-core/pkg/agently/reportshareartifact"
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
	CreateJob(ctx context.Context, job *reportjob.Record) error
	GetJob(ctx context.Context, jobID string) (*reportjob.Record, error)
	ListJobs(ctx context.Context) ([]*reportjob.Record, error)
	UpdateJob(ctx context.Context, job *reportjob.Record) error
	PutArtifact(ctx context.Context, artifact *reportartifact.Record) error
	GetArtifact(ctx context.Context, artifactID string) (*reportartifact.Record, error)
	ListArtifacts(ctx context.Context) ([]*reportartifact.Record, error)
	CreateSharedArtifact(ctx context.Context, artifact *reportshareartifact.Record) error
	GetSharedArtifact(ctx context.Context, artifactID string) (*reportshareartifact.Record, error)
	ListSharedArtifacts(ctx context.Context) ([]*reportshareartifact.Record, error)
	UpdateSharedArtifact(ctx context.Context, artifact *reportshareartifact.Record) error
	DeleteSharedArtifact(ctx context.Context, artifactID string) error
}

// RunClient persists durable browser report runs and their active conversation
// pointers. Implementations must scope reads and writes to the authenticated
// owner in ctx.
type RunClient interface {
	CreateReportRun(ctx context.Context, run *reportrun.Record) error
	GetReportRun(ctx context.Context, reportRunID string) (*reportrun.Record, error)
	GetReportRunByRequestID(ctx context.Context, uiRunRequestID string) (*reportrun.Record, error)
	UpdateReportRunCAS(ctx context.Context, run *reportrun.Record, expectedRevision int64) error
	GetConversationReportContext(ctx context.Context, conversationID string) (*reportcontext.Record, error)
	PutConversationReportContextCAS(ctx context.Context, record *reportcontext.Record, expectedRevision int64) error
	AdoptReportRunAndContextCAS(ctx context.Context, run *reportrun.Record, expectedRunRevision int64, record *reportcontext.Record, expectedContextRevision int64) error
}

// RunExportClient owns the T2 transactional/recoverable boundaries. The
// candidate job contains trusted identity and export options only;
// SubmitJobFromRun must copy the snapshot from the exact persisted run.
type RunExportClient interface {
	SubmitJobFromRun(ctx context.Context, candidate *reportjob.Record) (job *reportjob.Record, replay bool, err error)
	ClaimJob(ctx context.Context, jobID string, startedAt time.Time) (*reportjob.Record, error)
	CompleteJobWithArtifact(ctx context.Context, jobID string, artifact *reportartifact.Record, diagnostics []byte, completedAt time.Time, retentionTTL time.Duration) (*reportjob.Record, error)
	FailJob(ctx context.Context, jobID, errorText string, diagnostics []byte, completedAt time.Time) (*reportjob.Record, error)
	ReconcileRunningJobs(ctx context.Context, staleBefore, reconciledAt time.Time, errorText string) ([]*reportjob.Record, error)
}

// ValidateRunExportCandidate limits the trusted submit boundary to exact run
// identity plus the currently supported exporter choices. The run revision and
// snapshots are deliberately absent here: stores derive and copy them.
func ValidateRunExportCandidate(candidate *reportjob.Record) error {
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
func HasRunExportLink(job *reportjob.Record) bool {
	return job != nil &&
		(strings.TrimSpace(job.ReportRunID) != "" ||
			job.ReportRunRevision != 0 ||
			strings.TrimSpace(job.ExportRequestID) != "")
}
