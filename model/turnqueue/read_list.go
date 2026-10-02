package turnqueue

import (
	read "github.com/viant/agently-core/internal/datly/turnqueue/read"

	"github.com/viant/xdatly/response"
)

type QueueRowsInput struct {
	Id             string
	ConversationId string
	TurnId         string
	MessageId      string
	QueueStatus    string
	Has            *QueueRowsInputHas `setMarker:"true" format:"-" sqlx:"-" diff:"-" json:"-"`
}

type QueueRowsInputHas struct {
	Id             bool
	ConversationId bool
	TurnId         bool
	MessageId      bool
	QueueStatus    bool
}

type QueueRowsOutput struct {
	response.Status `json:",omitempty"`
	Data            []*QueueRowView
	Metrics         response.Metrics
}

type QueueRowView = read.QueueRowView

var QueueRowsPathURI = "/v1/api/agently/turnqueue/list"
