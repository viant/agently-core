package turn

import (
	"github.com/viant/xdatly/response"
)

// Public data shapes retained for Go caller and JSON compatibility.

type QueuedTotalInput struct {
	ConversationID string
	Has            *QueuedTotalInputHas `setMarker:"true" format:"-" sqlx:"-" diff:"-" json:"-"`
}

type QueuedTotalInputHas struct {
	ConversationID bool
}

type QueuedTotalOutput struct {
	response.Status `json:",omitempty"`
	Data            []*QueuedTotalView
	Metrics         response.Metrics
}

type QueuedTotalView struct {
	QueuedCount int `sqlx:"queued_count"`
}

var QueuedTotalPathURI = "/v1/api/agently/turn/queuedCount/queuedCount"
