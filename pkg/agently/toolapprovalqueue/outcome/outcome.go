package toolapprovalqueue

import (
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

type OutcomeRowView struct {
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
	TransitionAt     *string    `sqlx:"transition_at"`
}

var OutcomeRowsPathURI = "/v1/api/agently/toolapprovalqueue/outcome/outcome"
