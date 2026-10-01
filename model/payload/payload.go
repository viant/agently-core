package payload

import (
	read "github.com/viant/agently-core/internal/datly/payload/read"
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

type PayloadRowsView = read.PayloadRowsView

var PayloadRowsPathURI = "/v1/api/agently/payload"
