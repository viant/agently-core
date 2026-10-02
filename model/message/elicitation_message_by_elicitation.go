package message

import (
	"github.com/viant/xdatly/response"

	"time"
)

// Public data shapes retained for Go caller and JSON compatibility.

type ElicitationMessageInput struct {
	ConversationId string
	ElicitationId  string
	Has            *ElicitationMessageInputHas `setMarker:"true" format:"-" sqlx:"-" diff:"-" json:"-"`
}

type ElicitationMessageInputHas struct {
	ConversationId bool
	ElicitationId  bool
}

type ElicitationMessageOutput struct {
	response.Status `json:",omitempty"`
	Data            []*ElicitationMessageView
	Metrics         response.Metrics
}

type ElicitationMessageView struct {
	Id                   string     `sqlx:"id"`
	ConversationId       string     `sqlx:"conversation_id"`
	TurnId               *string    `sqlx:"turn_id"`
	Archived             *int       `sqlx:"archived"`
	Sequence             *int       `sqlx:"sequence"`
	CreatedAt            time.Time  `sqlx:"created_at"`
	UpdatedAt            *time.Time `sqlx:"updated_at"`
	CreatedByUserId      *string    `sqlx:"created_by_user_id"`
	Status               *string    `sqlx:"status"`
	Mode                 *string    `sqlx:"mode"`
	Role                 string     `sqlx:"role"`
	Type                 string     `sqlx:"type"`
	Content              *string    `sqlx:"content"`
	RawContent           *string    `sqlx:"raw_content"`
	Summary              *string    `sqlx:"summary"`
	ContextSummary       *string    `sqlx:"context_summary"`
	Tags                 *string    `sqlx:"tags"`
	Interim              int        `sqlx:"interim"`
	ElicitationId        *string    `sqlx:"elicitation_id"`
	ParentMessageId      *string    `sqlx:"parent_message_id"`
	SupersededBy         *string    `sqlx:"superseded_by"`
	LinkedConversationId *string    `sqlx:"linked_conversation_id"`
	AttachmentPayloadId  *string    `sqlx:"attachment_payload_id"`
	ElicitationPayloadId *string    `sqlx:"elicitation_payload_id"`
	ToolName             *string    `sqlx:"tool_name"`
	EmbeddingIndex       *string    `sqlx:"embedding_index"`
	Preamble             *string    `sqlx:"preamble"`
	Iteration            *int       `sqlx:"iteration"`
	Phase                *string    `sqlx:"phase"`
}

var ElicitationMessagePathURI = "/v1/api/agently/message/elicitation/elicitation/{convId}/{elicId}"
