package turn

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

// ControllerCount returns the number of controller-owned (autonomous) turns
// created for a conversation. It mirrors the QueuedTotal count component but
// filters on origin instead of status. It is used to derive
// AutonomousTurnsUsed for the goal controller snapshot.

func init() {
	core.RegisterType("turn", "ControllerTotalInput", reflect.TypeOf(ControllerTotalInput{}), checksum.GeneratedTime)
	core.RegisterType("turn", "ControllerTotalOutput", reflect.TypeOf(ControllerTotalOutput{}), checksum.GeneratedTime)
}

//go:embed controller_total/*.sql
var ControllerTotalFS embed.FS

type ControllerTotalInput struct {
	ConversationID string                   `parameter:",kind=query,in=conversationId" predicate:"equal,group=0,t,conversation_id"`
	Has            *ControllerTotalInputHas `setMarker:"true" format:"-" sqlx:"-" diff:"-" json:"-"`
}

type ControllerTotalInputHas struct {
	ConversationID bool
}

type ControllerTotalOutput struct {
	response.Status `parameter:",kind=output,in=status" json:",omitempty"`
	Data            []*ControllerTotalView `parameter:",kind=output,in=view" view:"controller_total,batch=10000,relationalConcurrency=1" sql:"uri=controller_total/controller_total.sql"`
	Metrics         response.Metrics       `parameter:",kind=output,in=metrics"`
}

type ControllerTotalView struct {
	ControllerCount int `sqlx:"controller_count"`
}

var ControllerTotalPathURI = "/v1/api/agently/turn/controllerCount/controllerCount"

func DefineControllerTotalComponent(ctx context.Context, srv *datly.Service) error {
	aComponent, err := repository.NewComponent(
		contract.NewPath("GET", ControllerTotalPathURI),
		repository.WithResource(srv.Resource()),
		repository.WithContract(
			reflect.TypeOf(ControllerTotalInput{}),
			reflect.TypeOf(ControllerTotalOutput{}), &ControllerTotalFS, view.WithConnectorRef("agently")))

	if err != nil {
		return fmt.Errorf("failed to create ControllerTotal component: %w", err)
	}
	if err := srv.AddComponent(ctx, aComponent); err != nil {
		return fmt.Errorf("failed to add ControllerTotal component: %w", err)
	}
	return nil
}

func (i *ControllerTotalInput) EmbedFS() *embed.FS {
	return &ControllerTotalFS
}
