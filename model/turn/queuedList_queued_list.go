package turn

import (
	"github.com/viant/xdatly/response"
)

// Public data shapes retained for Go caller and JSON compatibility.

type QueuedTurnsInput struct {
	ConversationID string
	Has            *QueuedTurnsInputHas `setMarker:"true" format:"-" sqlx:"-" diff:"-" json:"-"`
}

type QueuedTurnsInputHas struct {
	ConversationID bool
}

type QueuedTurnsOutput struct {
	response.Status `json:",omitempty"`
	Data            []*QueuedTurnsView
	Metrics         response.Metrics
}

type QueuedTurnsView struct {
	Id       string `sqlx:"id"`
	QueueSeq *int   `sqlx:"queue_seq"`
}

var QueuedTurnsPathURI = "/v1/api/agently/turn/queuedList/queuedList"
