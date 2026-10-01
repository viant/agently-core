package read

import (
	agmessage "github.com/viant/agently-core/pkg/agently/message"

	"github.com/viant/xdatly/response"
)

type MessageByLinkedConversationAndElicitationInput struct {
	LinkedConversationId string
	ElicitationId        string
	Has                  *MessageByLinkedConversationAndElicitationHas `setMarker:"true" format:"-" sqlx:"-" diff:"-" json:"-"`
}

type MessageByLinkedConversationAndElicitationHas struct {
	LinkedConversationId bool
	ElicitationId        bool
}

type MessageByLinkedConversationAndElicitationOutput struct {
	response.Status `json:",omitempty"`
	Data            []*agmessage.MessageView
	Metrics         response.Metrics
}

var MessageByLinkedConversationAndElicitationPathURI = "/v1/api/agently/message/by-linked-elicitation/{linkedConversationId}/{elicId}"
