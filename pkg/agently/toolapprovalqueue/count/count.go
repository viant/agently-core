package toolapprovalqueue

import (
	"github.com/viant/xdatly/response"
)

type QueueTotalInput struct {
	Id             string
	UserId         string
	ConversationId string
	TurnId         string
	MessageId      string
	ToolName       string
	QueueStatus    string
	Has            *QueueTotalInputHas `setMarker:"true" format:"-" sqlx:"-" diff:"-" json:"-"`
}

type QueueTotalInputHas struct {
	Id             bool
	UserId         bool
	ConversationId bool
	TurnId         bool
	MessageId      bool
	ToolName       bool
	QueueStatus    bool
}

type QueueTotalOutput struct {
	response.Status `json:",omitempty"`
	Data            []*QueueTotalView
	Metrics         response.Metrics
}

type QueueTotalView struct {
	TotalCount int `sqlx:"total_count"`
}

var QueueTotalPathURI = "/v1/api/agently/toolapprovalqueue/count/count"
