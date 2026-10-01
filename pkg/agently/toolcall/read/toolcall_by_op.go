package read

import (
	"github.com/viant/xdatly/response"
)

type ByOpInput struct {
	ConversationId string
	OpId           string
	Has            *ByOpInputHas `setMarker:"true" format:"-" sqlx:"-" diff:"-" json:"-"`
}

type ByOpInputHas struct {
	ConversationId bool
	OpId           bool
}

type ByOpOutput struct {
	response.Status `json:",omitempty"`
	Data            []*ToolCallRow
	Metrics         response.Metrics
}

type ToolCallRow struct {
	MessageId         string  `sqlx:"message_id"`
	TurnId            *string `sqlx:"turn_id"`
	OpId              string  `sqlx:"op_id"`
	TraceId           *string `sqlx:"trace_id"`
	ResponsePayloadId *string `sqlx:"response_payload_id"`
}

var PathURI = "/v1/api/agently/toolcall/by-op/{opId}"
