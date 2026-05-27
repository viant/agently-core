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

// Source of truth: dql/agently/toolapprovalqueue/count.dql
func init() {
	core.RegisterType("toolapprovalqueue", "QueueTotalInput", reflect.TypeOf(QueueTotalInput{}), checksum.GeneratedTime)
	core.RegisterType("toolapprovalqueue", "QueueTotalOutput", reflect.TypeOf(QueueTotalOutput{}), checksum.GeneratedTime)
}

//go:embed queue_total/*.sql
var QueueTotalFS embed.FS

type QueueTotalInput struct {
	Id             string              `parameter:",kind=query,in=id" predicate:"equal,group=0,q,id"`
	UserId         string              `parameter:",kind=query,in=userId" predicate:"equal,group=0,q,user_id"`
	ConversationId string              `parameter:",kind=query,in=conversationId" predicate:"equal,group=0,q,conversation_id"`
	TurnId         string              `parameter:",kind=query,in=turnId" predicate:"equal,group=0,q,turn_id"`
	MessageId      string              `parameter:",kind=query,in=messageId" predicate:"equal,group=0,q,message_id"`
	ToolName       string              `parameter:",kind=query,in=toolName" predicate:"equal,group=0,q,tool_name"`
	QueueStatus    string              `parameter:",kind=query,in=status" predicate:"equal,group=0,q,status"`
	Has            *QueueTotalInputHas `setMarker:"true" format:"-" sqlx:"-" diff:"-" json:"-"`
}

type QueueTotalInputHas struct {
	Id             bool
	UserId         bool
	ConversationId bool
	TurnId         bool
	MessageId      bool
	ToolName       bool
	QueueStatus    bool
}

type QueueTotalOutput struct {
	response.Status `parameter:",kind=output,in=apiStatus" json:",omitempty"`
	Data            []*QueueTotalView `parameter:",kind=output,in=view" view:"queue_total,batch=1000,relationalConcurrency=1" sql:"uri=queue_total/queue_total.sql"`
	Metrics         response.Metrics  `parameter:",kind=output,in=metrics"`
}

type QueueTotalView struct {
	TotalCount int `sqlx:"total_count"`
}

var QueueTotalPathURI = "/v1/api/agently/toolapprovalqueue/count/count"

func DefineQueueTotalComponent(ctx context.Context, srv *datly.Service) error {
	aComponent, err := repository.NewComponent(
		contract.NewPath("GET", QueueTotalPathURI),
		repository.WithResource(srv.Resource()),
		repository.WithContract(
			reflect.TypeOf(QueueTotalInput{}),
			reflect.TypeOf(QueueTotalOutput{}),
			&QueueTotalFS,
			view.WithConnectorRef("agently"),
		),
	)
	if err != nil {
		return fmt.Errorf("failed to create QueueTotal component: %w", err)
	}
	if err := srv.AddComponent(ctx, aComponent); err != nil {
		return fmt.Errorf("failed to add QueueTotal component: %w", err)
	}
	return nil
}

func (i *QueueTotalInput) EmbedFS() *embed.FS { return &QueueTotalFS }
