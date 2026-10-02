package toolcall

import (
	"github.com/viant/xdatly/response"
)

// Public data shapes retained for Go caller and JSON compatibility.

type ToolCallByTurnInput struct {
	ConversationId string
	TurnId         string
	Has            *ToolCallByTurnInputHas `setMarker:"true" format:"-" sqlx:"-" diff:"-" json:"-"`
}

type ToolCallByTurnInputHas struct {
	ConversationId bool
	TurnId         bool
}

type ToolCallByTurnOutput struct {
	response.Status `json:",omitempty"`
	Data            []*ToolCallByTurnView
	Metrics         response.Metrics
}

type ToolCallByTurnView struct {
	MessageId string  `sqlx:"message_id"`
	TurnId    *string `sqlx:"turn_id"`
	OpId      string  `sqlx:"op_id"`
	Attempt   int     `sqlx:"attempt"`
}

var ToolCallByTurnPathURI = "/v1/api/agently/toolcall/byTurn/by-turn"
