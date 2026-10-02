package toolcall

import (
	"github.com/viant/xdatly/response"
)

// Public data shapes retained for Go caller and JSON compatibility.

type ToolCallByOpInput struct {
	ConversationId string
	OpId           string
	Has            *ToolCallByOpInputHas `setMarker:"true" format:"-" sqlx:"-" diff:"-" json:"-"`
}

type ToolCallByOpInputHas struct {
	ConversationId bool
	OpId           bool
}

type ToolCallByOpOutput struct {
	response.Status `json:",omitempty"`
	Data            []*ToolCallByOpView
	Metrics         response.Metrics
}

type ToolCallByOpView struct {
	MessageId         string  `sqlx:"message_id"`
	TurnId            *string `sqlx:"turn_id"`
	OpId              string  `sqlx:"op_id"`
	TraceId           *string `sqlx:"trace_id"`
	ResponsePayloadId *string `sqlx:"response_payload_id"`
}

var ToolCallByOpPathURI = "/v1/api/agently/toolcall/byOp/by-op/{opId}"
