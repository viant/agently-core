package conversation

import read "github.com/viant/agently-core/internal/datly/conversation/read"

type (
	ConversationRowsInput    = read.ConversationRequest
	ConversationRowsInputHas = read.ConversationRequestHas
	ConversationRowsOutput   = read.ConversationOutput
	ConversationRowsView     = read.ConversationView
)

var ConversationRowsPathURI = "/v1/api/agently/conversation/list/list"
