package read

import (
	"context"

	message "github.com/viant/agently-core/pkg/agently/message/elicitation"
	"github.com/viant/datly"
)

type MessageByElicitationInput = message.MessageInput
type MessageByElicitationOutput = message.MessageOutput

type MessageByElicitationView = message.MessageView

var MessageByElicitationPathURI = message.MessagePathURI

func DefineMessageByElicitationComponent(ctx context.Context, srv *datly.Service) error {
	return message.DefineMessageComponent(ctx, srv)
}
