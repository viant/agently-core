package read

import (
	"time"

	"github.com/viant/xdatly/response"
)

type Input struct {
	TenantID string
	Id       string
	Ids      []string
	Kind     string
	Digest   string
	Storage  string
	MimeType string
	Since    *time.Time
	Has      *Has `setMarker:"true" format:"-" sqlx:"-" diff:"-" json:"-"`
}

type Has struct {
	TenantID bool
	Id       bool
	Ids      bool
	Kind     bool
	Digest   bool
	Storage  bool
	MimeType bool
	Since    bool
}

type Output struct {
	response.Status `json:",omitempty"`
	Data            []*PayloadView
	Metrics         response.Metrics
}

type PayloadView struct {
	Id                     string     `sqlx:"id"`
	TenantID               *string    `sqlx:"tenant_id"`
	Kind                   string     `sqlx:"kind"`
	Subtype                *string    `sqlx:"subtype"`
	MimeType               string     `sqlx:"mime_type"`
	SizeBytes              int        `sqlx:"size_bytes"`
	Digest                 *string    `sqlx:"digest"`
	Storage                string     `sqlx:"storage"`
	InlineBody             *[]byte    `sqlx:"inline_body"`
	URI                    *string    `sqlx:"uri"`
	Compression            string     `sqlx:"compression"`
	EncryptionKMSKeyID     *string    `sqlx:"encryption_kms_key_id"`
	RedactionPolicyVersion *string    `sqlx:"redaction_policy_version"`
	Redacted               *int       `sqlx:"redacted"`
	CreatedAt              *time.Time `sqlx:"created_at"`
	SchemaRef              *string    `sqlx:"schema_ref"`
}

var PayloadURI = "/v2/api/agently/payload"
