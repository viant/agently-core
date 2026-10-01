package read

import (
	"github.com/viant/agently-core/pkg/agently/conversation"

	"github.com/viant/xdatly/response"
)

type MessageInput struct {
	Id              string
	IncludeModelCal bool
	IncludeToolCall bool
	Has             *MessageInputHas `setMarker:"true" format:"-" sqlx:"-" diff:"-" json:"-"`
}

type MessageInputHas struct {
	Id              bool
	IncludeModelCal bool
	IncludeToolCall bool
}

type MessageOutput struct {
	response.Status `json:",omitempty"`
	Data            []*conversation.MessageView
	Metrics         response.Metrics
}

var MessagePathURI = "/v1/api/agently/message/{id}"
