package conversation

import read "github.com/viant/agently-core/internal/datly/conversation/read"

// Public names alias the contracts authored by the v1 conversation DQL.
type (
	ConversationInput          = read.ConversationRequest
	ConversationInputHas       = read.ConversationRequestHas
	ConversationOutput         = read.ConversationOutput
	ConversationView           = read.ConversationView
	TranscriptView             = read.TranscriptView
	MessageView                = read.MessageView
	ToolMessageView            = read.ToolMessageView
	ToolCallView               = read.ToolCallView
	MessageToolCallView        = read.MessageToolCallView
	ModelCallStreamPayloadView = read.ModelCallStreamPayloadView
	UserElicitationDataView    = read.UserElicitationDataView
	LinkedConversationView     = read.LinkedConversationView
	AttachmentView             = read.AttachmentView
	ModelCallView              = read.ModelCallView
	ToolCallLinksView          = read.ToolCallLinksView
	UsageView                  = read.UsageView
	ModelView                  = read.ModelView
)
