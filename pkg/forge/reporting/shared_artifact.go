package reporting

import "time"

// SharedArtifact is the Datly-facing persisted report shell used by the SQL
// reporting store.
type SharedArtifact struct {
	ArtifactID       string     `sqlx:"artifact_id,primaryKey" json:"artifactId,omitempty"`
	ArtifactRef      string     `sqlx:"artifact_ref" json:"artifactRef,omitempty"`
	OwnerID          string     `sqlx:"owner_id" json:"ownerId,omitempty"`
	OwnerRef         string     `sqlx:"owner_ref" json:"ownerRef,omitempty"`
	Kind             string     `sqlx:"kind" json:"kind,omitempty"`
	Lifecycle        string     `sqlx:"lifecycle" json:"lifecycle,omitempty"`
	Version          int        `sqlx:"version" json:"version,omitempty"`
	ReportID         string     `sqlx:"report_id" json:"reportId,omitempty"`
	Title            string     `sqlx:"title" json:"title,omitempty"`
	SourceArtifactID string     `sqlx:"source_artifact_id" json:"sourceArtifactId,omitempty"`
	BaseArtifactRef  string     `sqlx:"base_artifact_ref" json:"baseArtifactRef,omitempty"`
	PolicyRef        string     `sqlx:"policy_ref" json:"policyRef,omitempty"`
	DocumentVersion  int        `sqlx:"document_version" json:"documentVersion,omitempty"`
	Document         []byte     `sqlx:"report_document_json" json:"document,omitempty"`
	ReportSpec       []byte     `sqlx:"report_spec_json" json:"reportSpec,omitempty"`
	CompileState     []byte     `sqlx:"compile_state_json" json:"compileState,omitempty"`
	ReportFill       []byte     `sqlx:"report_fill_json" json:"reportFill,omitempty"`
	ReportPrint      []byte     `sqlx:"report_print_json" json:"reportPrint,omitempty"`
	SavedViewOverlay []byte     `sqlx:"saved_view_overlay_json" json:"savedViewOverlay,omitempty"`
	Metadata         []byte     `sqlx:"metadata_json" json:"metadata,omitempty"`
	CreatedAt        time.Time  `sqlx:"created_at" json:"createdAt,omitempty"`
	UpdatedAt        *time.Time `sqlx:"updated_at" json:"updatedAt,omitempty"`
}
