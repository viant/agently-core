package message

import (
	"context"
	"embed"
	"fmt"
	"reflect"

	"github.com/viant/datly"
	"github.com/viant/datly/repository"
	"github.com/viant/datly/repository/contract"
	"github.com/viant/datly/view"
	"github.com/viant/xdatly/handler/response"
	"github.com/viant/xdatly/types/core"
	"github.com/viant/xdatly/types/custom/dependency/checksum"
)

// ElicitationPendingCount returns the number of unresolved elicitation request
// messages for a conversation. An elicitation request is persisted with
// status='pending' and flips to a terminal status when resolved, so a simple
// status filter yields the pending count. It is used to derive the
// PendingElicitation guard for the goal controller snapshot.

func init() {
	core.RegisterType("message", "ElicitationPendingInput", reflect.TypeOf(ElicitationPendingInput{}), checksum.GeneratedTime)
	core.RegisterType("message", "ElicitationPendingOutput", reflect.TypeOf(ElicitationPendingOutput{}), checksum.GeneratedTime)
}

//go:embed elicitation_total/*.sql
var ElicitationPendingFS embed.FS

type ElicitationPendingInput struct {
	ConversationID string                      `parameter:",kind=query,in=conversationId" predicate:"equal,group=0,m,conversation_id"`
	Has            *ElicitationPendingInputHas `setMarker:"true" format:"-" sqlx:"-" diff:"-" json:"-"`
}

type ElicitationPendingInputHas struct {
	ConversationID bool
}

type ElicitationPendingOutput struct {
	response.Status `parameter:",kind=output,in=status" json:",omitempty"`
	Data            []*ElicitationPendingView `parameter:",kind=output,in=view" view:"elicitation_pending_total,batch=10000,relationalConcurrency=1" sql:"uri=elicitation_total/elicitation_total.sql"`
	Metrics         response.Metrics          `parameter:",kind=output,in=metrics"`
}

type ElicitationPendingView struct {
	PendingCount int `sqlx:"pending_count"`
}

var ElicitationPendingPathURI = "/v1/api/agently/message/elicitationCount/elicitationCount"

func DefineElicitationPendingComponent(ctx context.Context, srv *datly.Service) error {
	aComponent, err := repository.NewComponent(
		contract.NewPath("GET", ElicitationPendingPathURI),
		repository.WithResource(srv.Resource()),
		repository.WithContract(
			reflect.TypeOf(ElicitationPendingInput{}),
			reflect.TypeOf(ElicitationPendingOutput{}), &ElicitationPendingFS, view.WithConnectorRef("agently")))

	if err != nil {
		return fmt.Errorf("failed to create ElicitationPending component: %w", err)
	}
	if err := srv.AddComponent(ctx, aComponent); err != nil {
		return fmt.Errorf("failed to add ElicitationPending component: %w", err)
	}
	return nil
}

func (i *ElicitationPendingInput) EmbedFS() *embed.FS {
	return &ElicitationPendingFS
}
