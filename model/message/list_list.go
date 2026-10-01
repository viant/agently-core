package message

import (
	"github.com/viant/xdatly/response"

	"time"
)

// Public data shapes retained for Go caller and JSON compatibility.

type MessageRowsInput struct {
	ConversationId  string
	TurnId          string
	Id              string
	Roles           []string
	Types           []string
	Interim         int
	Phase           string
	Iteration       int
	CreatedSince    time.Time
	CreatedBefore   time.Time
	CursorBefore    string
	CursorAfter     string
	TurnTask        bool
	AssistantFinal  bool
	AssistantStatus bool
	IncludeModelCal bool
	IncludeToolCall bool
	Has             *MessageRowsInputHas `setMarker:"true" format:"-" sqlx:"-" diff:"-" json:"-"`
}

type MessageRowsInputHas struct {
	ConversationId  bool
	TurnId          bool
	Id              bool
	Roles           bool
	Types           bool
	Interim         bool
	Phase           bool
	Iteration       bool
	CreatedSince    bool
	CreatedBefore   bool
	CursorBefore    bool
	CursorAfter     bool
	TurnTask        bool
	AssistantFinal  bool
	AssistantStatus bool
	IncludeModelCal bool
	IncludeToolCall bool
}

type MessageRowsOutput struct {
	response.Status `json:",omitempty"`
	Data            []*MessageRowsView
	Metrics         response.Metrics
}

type MessageRowsView struct {
	Archived             *int       `sqlx:"archived"`
	AttachmentPayloadId  *string    `sqlx:"attachment_payload_id"`
	Content              *string    `sqlx:"content"`
	ContextSummary       *string    `sqlx:"context_summary"`
	ConversationId       string     `sqlx:"conversation_id"`
	CreatedAt            time.Time  `sqlx:"created_at"`
	CreatedByUserId      *string    `sqlx:"created_by_user_id"`
	ElicitationId        *string    `sqlx:"elicitation_id"`
	ElicitationPayloadId *string    `sqlx:"elicitation_payload_id"`
	EmbeddingIndex       *string    `sqlx:"embedding_index"`
	Id                   string     `sqlx:"id"`
	Interim              int        `sqlx:"interim"`
	Iteration            *int       `sqlx:"iteration"`
	LinkedConversationId *string    `sqlx:"linked_conversation_id"`
	Mode                 *string    `sqlx:"mode"`
	ParentMessageId      *string    `sqlx:"parent_message_id"`
	Phase                *string    `sqlx:"phase"`
	Narration            *string    `sqlx:"preamble"`
	RawContent           *string    `sqlx:"raw_content"`
	Role                 string     `sqlx:"role"`
	Sequence             *int       `sqlx:"sequence"`
	Status               *string    `sqlx:"status"`
	Summary              *string    `sqlx:"summary"`
	SupersededBy         *string    `sqlx:"superseded_by"`
	Tags                 *string    `sqlx:"tags"`
	ToolName             *string    `sqlx:"tool_name"`
	TurnId               *string    `sqlx:"turn_id"`
	Type                 string     `sqlx:"type"`
	UpdatedAt            *time.Time `sqlx:"updated_at"`
}

var MessageRowsPathURI = "/v1/api/agently/message/list/list"
