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

type MessageByParentAndElicitationInput struct {
	ParentMessageId string                                 `parameter:",kind=path,in=parentMessageId" predicate:"equal,group=4,m,parent_message_id"`
	ElicitationId   string                                 `parameter:",kind=path,in=elicId" predicate:"equal,group=4,m,elicitation_id"`
	Has             *MessageByParentAndElicitationInputHas `setMarker:"true" format:"-" sqlx:"-" diff:"-" json:"-"`
}

type MessageByParentAndElicitationInputHas struct {
	ParentMessageId bool
	ElicitationId   bool
}

type MessageByParentAndElicitationOutput struct {
	response.Status `parameter:",kind=output,in=status" json:",omitempty"`
	Data            []*agmessage.MessageView `parameter:",kind=output,in=view" view:"message,batch=10000,relationalConcurrency=1" sql:"uri=message/message.sql"`
	Metrics         response.Metrics         `parameter:",kind=output,in=metrics"`
}

var MessageByParentAndElicitationPathURI = "/v1/api/agently/message/by-parent-elicitation/{parentMessageId}/{elicId}"

func DefineMessageByParentAndElicitationComponent(ctx context.Context, srv *datly.Service) error {
	aComponent, err := repository.NewComponent(
		contract.NewPath("GET", MessageByParentAndElicitationPathURI),
		repository.WithResource(srv.Resource()),
		repository.WithContract(
			reflect.TypeOf(MessageByParentAndElicitationInput{}),
			reflect.TypeOf(MessageByParentAndElicitationOutput{}), &agmessage.MessageFS, view.WithConnectorRef("agently")))

	if err != nil {
		return fmt.Errorf("failed to create MessageByParentAndElicitation component: %w", err)
	}
	if err := srv.AddComponent(ctx, aComponent); err != nil {
		return fmt.Errorf("failed to add MessageByParentAndElicitation component: %w", err)
	}
	return nil
}

func (i *MessageByParentAndElicitationInput) EmbedFS() *embed.FS {
	return &agmessage.MessageFS
}
