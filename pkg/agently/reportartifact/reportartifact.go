package reportartifact

import "time"

// Record is the persisted downloadable reporting artifact owned by
// agently-core.
type Record struct {
	ArtifactID   string        `json:"artifactId,omitempty"`
	JobID        string        `json:"jobId,omitempty"`
	ArtifactRef  string        `json:"artifactRef,omitempty"`
	OwnerID      string        `json:"ownerId,omitempty"`
	Format       string        `json:"format,omitempty"`
	ContentType  string        `json:"contentType,omitempty"`
	Data         []byte        `json:"data,omitempty"`
	CreatedAt    time.Time     `json:"createdAt,omitempty"`
	RetentionTTL time.Duration `json:"retentionTtl,omitempty"`
}
