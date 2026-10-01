package payload

import (
	"github.com/viant/xdatly/response"

	"time"
)

// Public data shapes retained for Go caller and JSON compatibility.

type PayloadRowsInput struct {
	TenantID string
	Id       string
	Ids      []string
	Kind     string
	Digest   string
	Storage  string
	MimeType string
	Since    time.Time
	Has      *PayloadRowsInputHas `setMarker:"true" format:"-" sqlx:"-" diff:"-" json:"-"`
}

type PayloadRowsInputHas struct {
	TenantID bool
	Id       bool
	Ids      bool
	Kind     bool
	Digest   bool
	Storage  bool
	MimeType bool
	Since    bool
}

type PayloadRowsOutput struct {
	response.Status `json:",omitempty"`
	Data            []*PayloadRowsView
	Metrics         response.Metrics
}

type PayloadRowsView struct {
	Id                     string    `sqlx:"id"`
	TenantId               *string   `sqlx:"tenant_id"`
	Kind                   string    `sqlx:"kind"`
	Subtype                *string   `sqlx:"subtype"`
	MimeType               string    `sqlx:"mime_type"`
	SizeBytes              int       `sqlx:"size_bytes"`
	Digest                 *string   `sqlx:"digest"`
	Storage                string    `sqlx:"storage"`
	InlineBody             *string   `sqlx:"inline_body"`
	Uri                    *string   `sqlx:"uri"`
	Compression            string    `sqlx:"compression"`
	EncryptionKmsKeyId     *string   `sqlx:"encryption_kms_key_id"`
	RedactionPolicyVersion *string   `sqlx:"redaction_policy_version"`
	Redacted               int       `sqlx:"redacted"`
	CreatedAt              time.Time `sqlx:"created_at"`
	SchemaRef              *string   `sqlx:"schema_ref"`
}

var PayloadRowsPathURI = "/v1/api/agently/payload"
