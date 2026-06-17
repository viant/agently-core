package reportjob

import "time"

// Record is the persisted reporting export job state owned by agently-core.
type Record struct {
	JobID        string        `json:"jobId,omitempty"`
	ArtifactRef  string        `json:"artifactRef,omitempty"`
	OwnerID      string        `json:"ownerId,omitempty"`
	Format       string        `json:"format,omitempty"`
	Scope        string        `json:"scope,omitempty"`
	Status       string        `json:"status,omitempty"`
	ReportSpec   []byte        `json:"reportSpec,omitempty"`
	ReportFill   []byte        `json:"reportFill,omitempty"`
	ReportPrint  []byte        `json:"reportPrint,omitempty"`
	ArtifactID   string        `json:"artifactId,omitempty"`
	Error        string        `json:"error,omitempty"`
	Diagnostics  []byte        `json:"diagnostics,omitempty"`
	SubmittedAt  time.Time     `json:"submittedAt,omitempty"`
	StartedAt    *time.Time    `json:"startedAt,omitempty"`
	CompletedAt  *time.Time    `json:"completedAt,omitempty"`
	RetentionTTL time.Duration `json:"retentionTtl,omitempty"`
}
