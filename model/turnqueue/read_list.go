package turnqueue

import (
	"time"

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

type QueueRowView struct {
	Id             string     `sqlx:"id"`
	ConversationId string     `sqlx:"conversation_id"`
	TurnId         string     `sqlx:"turn_id"`
	MessageId      string     `sqlx:"message_id"`
	QueueSeq       int64      `sqlx:"queue_seq"`
	Status         string     `sqlx:"status"`
	CreatedAt      time.Time  `sqlx:"created_at"`
	UpdatedAt      *time.Time `sqlx:"updated_at"`
}

var QueueRowsPathURI = "/v1/api/agently/turnqueue/list"
