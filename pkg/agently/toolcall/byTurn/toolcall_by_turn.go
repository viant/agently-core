package toolcall

import (
	"github.com/viant/xdatly/response"
)

// Public data shapes retained for Go caller and JSON compatibility.

type ToolCallRowsInput struct {
	ConversationId string
	TurnId         string
	Has            *ToolCallRowsInputHas `setMarker:"true" format:"-" sqlx:"-" diff:"-" json:"-"`
}

type ToolCallRowsInputHas struct {
	ConversationId bool
	TurnId         bool
}

type ToolCallRowsOutput struct {
	response.Status `json:",omitempty"`
	Data            []*ToolCallRowsView
	Metrics         response.Metrics
}

type ToolCallRowsView struct {
	MessageId string  `sqlx:"message_id"`
	TurnId    *string `sqlx:"turn_id"`
	OpId      string  `sqlx:"op_id"`
	Attempt   int     `sqlx:"attempt"`
}

var ToolCallRowsPathURI = "/v1/api/agently/toolcall/byTurn/by-turn"
