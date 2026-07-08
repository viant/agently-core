package reportshareartifact

import "time"

// Record is the persisted shared reporting artifact shell for saved views and
// published snapshots. The payload remains opaque JSON so agently-core can own
// storage without reinterpreting Forge authoring semantics in this layer.
type Record struct {
	ArtifactID       string     `json:"artifactId,omitempty"`
	ArtifactRef      string     `json:"artifactRef,omitempty"`
	OwnerID          string     `json:"ownerId,omitempty"`
	Kind             string     `json:"kind,omitempty"`
	Lifecycle        string     `json:"lifecycle,omitempty"`
	Version          int        `json:"version,omitempty"`
	ReportID         string     `json:"reportId,omitempty"`
	Title            string     `json:"title,omitempty"`
	SourceArtifactID string     `json:"sourceArtifactId,omitempty"`
	BaseArtifactRef  string     `json:"baseArtifactRef,omitempty"`
	PolicyRef        string     `json:"policyRef,omitempty"`
	DocumentVersion  int        `json:"documentVersion,omitempty"`
	Document         []byte     `json:"document,omitempty"`
	ReportSpec       []byte     `json:"reportSpec,omitempty"`
	CompileState     []byte     `json:"compileState,omitempty"`
	ReportFill       []byte     `json:"reportFill,omitempty"`
	ReportPrint      []byte     `json:"reportPrint,omitempty"`
	SavedViewOverlay []byte     `json:"savedViewOverlay,omitempty"`
	Metadata         []byte     `json:"metadata,omitempty"`
	CreatedAt        time.Time  `json:"createdAt,omitempty"`
	UpdatedAt        *time.Time `json:"updatedAt,omitempty"`
}
