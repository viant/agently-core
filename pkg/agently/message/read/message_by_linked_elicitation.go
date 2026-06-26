package read

import (
	"context"
	"embed"
	"fmt"
	"reflect"

	agmessage "github.com/viant/agently-core/pkg/agently/message"
	"github.com/viant/datly"
	"github.com/viant/datly/repository"
	"github.com/viant/datly/repository/contract"
	"github.com/viant/datly/view"
	"github.com/viant/xdatly/handler/response"
)

type MessageByLinkedConversationAndElicitationInput struct {
	LinkedConversationId string                                        `parameter:",kind=path,in=linkedConversationId" predicate:"equal,group=4,m,linked_conversation_id"`
	ElicitationId        string                                        `parameter:",kind=path,in=elicId" predicate:"equal,group=4,m,elicitation_id"`
	Has                  *MessageByLinkedConversationAndElicitationHas `setMarker:"true" format:"-" sqlx:"-" diff:"-" json:"-"`
}

type MessageByLinkedConversationAndElicitationHas struct {
	LinkedConversationId bool
	ElicitationId        bool
}

type MessageByLinkedConversationAndElicitationOutput struct {
	response.Status `parameter:",kind=output,in=status" json:",omitempty"`
	Data            []*agmessage.MessageView `parameter:",kind=output,in=view" view:"message,batch=10000,relationalConcurrency=1" sql:"uri=message/message.sql"`
	Metrics         response.Metrics         `parameter:",kind=output,in=metrics"`
}

var MessageByLinkedConversationAndElicitationPathURI = "/v1/api/agently/message/by-linked-elicitation/{linkedConversationId}/{elicId}"

func DefineMessageByLinkedConversationAndElicitationComponent(ctx context.Context, srv *datly.Service) error {
	aComponent, err := repository.NewComponent(
		contract.NewPath("GET", MessageByLinkedConversationAndElicitationPathURI),
		repository.WithResource(srv.Resource()),
		repository.WithContract(
			reflect.TypeOf(MessageByLinkedConversationAndElicitationInput{}),
			reflect.TypeOf(MessageByLinkedConversationAndElicitationOutput{}), &agmessage.MessageFS, view.WithConnectorRef("agently")))

	if err != nil {
		return fmt.Errorf("failed to create MessageByLinkedConversationAndElicitation component: %w", err)
	}
	if err := srv.AddComponent(ctx, aComponent); err != nil {
		return fmt.Errorf("failed to add MessageByLinkedConversationAndElicitation component: %w", err)
	}
	return nil
}

func (i *MessageByLinkedConversationAndElicitationInput) EmbedFS() *embed.FS {
	return &agmessage.MessageFS
}
