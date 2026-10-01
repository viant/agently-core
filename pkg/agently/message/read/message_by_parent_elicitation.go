package read

import (
	agmessage "github.com/viant/agently-core/pkg/agently/message"

	"github.com/viant/xdatly/response"
)

type MessageByParentAndElicitationInput struct {
	ParentMessageId string
	ElicitationId   string
	Has             *MessageByParentAndElicitationInputHas `setMarker:"true" format:"-" sqlx:"-" diff:"-" json:"-"`
}

type MessageByParentAndElicitationInputHas struct {
	ParentMessageId bool
	ElicitationId   bool
}

type MessageByParentAndElicitationOutput struct {
	response.Status `json:",omitempty"`
	Data            []*agmessage.MessageView
	Metrics         response.Metrics
}

var MessageByParentAndElicitationPathURI = "/v1/api/agently/message/by-parent-elicitation/{parentMessageId}/{elicId}"
