package toolapprovalqueue

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

// PendingCount returns the number of pending tool approvals for a conversation.
// It mirrors the turn QueuedTotal count component and is used to derive the
// PendingApproval guard for the goal controller snapshot.

func init() {
	core.RegisterType("toolapprovalqueue", "PendingTotalInput", reflect.TypeOf(PendingTotalInput{}), checksum.GeneratedTime)
	core.RegisterType("toolapprovalqueue", "PendingTotalOutput", reflect.TypeOf(PendingTotalOutput{}), checksum.GeneratedTime)
}

//go:embed pending_total/*.sql
var PendingTotalFS embed.FS

type PendingTotalInput struct {
	ConversationID string                `parameter:",kind=query,in=conversationId" predicate:"equal,group=0,q,conversation_id"`
	Has            *PendingTotalInputHas `setMarker:"true" format:"-" sqlx:"-" diff:"-" json:"-"`
}

type PendingTotalInputHas struct {
	ConversationID bool
}

type PendingTotalOutput struct {
	response.Status `parameter:",kind=output,in=status" json:",omitempty"`
	Data            []*PendingTotalView `parameter:",kind=output,in=view" view:"approval_pending_total,batch=10000,relationalConcurrency=1" sql:"uri=pending_total/pending_total.sql"`
	Metrics         response.Metrics    `parameter:",kind=output,in=metrics"`
}

type PendingTotalView struct {
	PendingCount int `sqlx:"pending_count"`
}

var PendingTotalPathURI = "/v1/api/agently/toolapprovalqueue/pendingCount/pendingCount"

func DefinePendingTotalComponent(ctx context.Context, srv *datly.Service) error {
	aComponent, err := repository.NewComponent(
		contract.NewPath("GET", PendingTotalPathURI),
		repository.WithResource(srv.Resource()),
		repository.WithContract(
			reflect.TypeOf(PendingTotalInput{}),
			reflect.TypeOf(PendingTotalOutput{}), &PendingTotalFS, view.WithConnectorRef("agently")))

	if err != nil {
		return fmt.Errorf("failed to create approval PendingTotal component: %w", err)
	}
	if err := srv.AddComponent(ctx, aComponent); err != nil {
		return fmt.Errorf("failed to add approval PendingTotal component: %w", err)
	}
	return nil
}

func (i *PendingTotalInput) EmbedFS() *embed.FS {
	return &PendingTotalFS
}
