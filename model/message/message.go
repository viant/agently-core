package message

import read "github.com/viant/agently-core/internal/datly/message/read"

// Public names alias the contracts authored by the v1 message DQL.
type (
	MessageInput    = read.MessageRequest
	MessageInputHas = read.MessageRequestHas
	MessageOutput   = read.MessagesOutput
	MessageView     = read.MessageView
)

var MessagePathURI = "/v1/api/agently/message/{id}"
