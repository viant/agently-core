package toolapprovalqueue

import (
	read "github.com/viant/agently-core/internal/datly/toolapprovalqueue/read"
	"time"

	"github.com/viant/xdatly/response"
)

type OutcomeRowsInput struct {
	UserId         string
	ConversationId string
	Since          time.Time
	Until          time.Time
	Has            *OutcomeRowsInputHas `setMarker:"true" format:"-" sqlx:"-" diff:"-" json:"-"`
}

type OutcomeRowsInputHas struct {
	UserId         bool
	ConversationId bool
	Since          bool
	Until          bool
}

type OutcomeRowsOutput struct {
	response.Status `json:",omitempty"`
	Data            []*OutcomeRowView
	Metrics         response.Metrics
}

type OutcomeRowView = read.ApprovalView

var OutcomeRowsPathURI = "/v1/api/agently/toolapprovalqueue/outcome/outcome"
