package read

import (
	"time"

	"github.com/viant/xdatly/response"
)

type QueueRowsInput struct {
	Id             string
	UserId         string
	ConversationId string
	TurnId         string
	MessageId      string
	ToolName       string
	QueueStatus    string
	Has            *QueueRowsInputHas `setMarker:"true" format:"-" sqlx:"-" diff:"-" json:"-"`
}

type QueueRowsInputHas struct {
	Id             bool
	UserId         bool
	ConversationId bool
	TurnId         bool
	MessageId      bool
	ToolName       bool
	QueueStatus    bool
}

type QueueRowsOutput struct {
	response.Status `json:",omitempty"`
	Data            []*QueueRowView
	Metrics         response.Metrics
}

type QueueRowView struct {
	Id               string     `sqlx:"id"`
	UserId           string     `sqlx:"user_id"`
	ConversationId   *string    `sqlx:"conversation_id"`
	TurnId           *string    `sqlx:"turn_id"`
	MessageId        *string    `sqlx:"message_id"`
	ToolName         string     `sqlx:"tool_name"`
	Title            *string    `sqlx:"title"`
	Arguments        []byte     `sqlx:"arguments"`
	Metadata         *[]byte    `sqlx:"metadata"`
	Status           string     `sqlx:"status"`
	Decision         *string    `sqlx:"decision"`
	ExpiresAt        *time.Time `sqlx:"expires_at"`
	TimedOutAt       *time.Time `sqlx:"timed_out_at"`
	ApprovedByUserId *string    `sqlx:"approved_by_user_id"`
	ApprovedAt       *time.Time `sqlx:"approved_at"`
	ExecutedAt       *time.Time `sqlx:"executed_at"`
	ErrorMessage     *string    `sqlx:"error_message"`
	CreatedAt        time.Time  `sqlx:"created_at"`
	UpdatedAt        *time.Time `sqlx:"updated_at"`
}

var QueueRowsPathURI = "/v1/api/agently/toolapprovalqueue/list"
