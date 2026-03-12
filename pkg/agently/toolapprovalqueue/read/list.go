package read

import (
	"context"
	"embed"
	"fmt"
	"reflect"
	"time"

	"github.com/viant/agently-core/internal/datlycompat"
	"github.com/viant/datly"
	"github.com/viant/datly/repository"
	"github.com/viant/datly/repository/contract"
	"github.com/viant/datly/view"
	"github.com/viant/xdatly/handler/response"
	"github.com/viant/xdatly/types/core"
	"github.com/viant/xdatly/types/custom/dependency/checksum"
)

func init() {
	core.RegisterType("toolapprovalqueue", "QueueRowsInput", reflect.TypeOf(QueueRowsInput{}), checksum.GeneratedTime)
	core.RegisterType("toolapprovalqueue", "QueueRowsOutput", reflect.TypeOf(QueueRowsOutput{}), checksum.GeneratedTime)
}

//go:embed queue_rows/*.sql
var QueueRowsFS embed.FS

type QueueRowsInput struct {
	Id             string             `parameter:",kind=query,in=id" predicate:"equal,group=0,q,id"`
	UserId         string             `parameter:",kind=query,in=userId" predicate:"equal,group=0,q,user_id"`
	ConversationId string             `parameter:",kind=query,in=conversationId" predicate:"equal,group=0,q,conversation_id"`
	TurnId         string             `parameter:",kind=query,in=turnId" predicate:"equal,group=0,q,turn_id"`
	MessageId      string             `parameter:",kind=query,in=messageId" predicate:"equal,group=0,q,message_id"`
	ToolName       string             `parameter:",kind=query,in=toolName" predicate:"equal,group=0,q,tool_name"`
	QueueStatus    string             `parameter:",kind=query,in=status" predicate:"equal,group=0,q,status"`
	Has            *QueueRowsInputHas `setMarker:"true" format:"-" sqlx:"-" diff:"-" json:"-"`
}

type QueueRowsInputHas struct {
	Id             bool
	UserId         bool
	ConversationId bool
	TurnId         bool
	MessageId      bool
	ToolName       bool
	QueueStatus    bool
}

type QueueRowsOutput struct {
	response.Status `parameter:",kind=output,in=apiStatus" json:",omitempty"`
	Data            []*QueueRowView  `parameter:",kind=output,in=view" view:"queue_rows,batch=1000,relationalConcurrency=1" sql:"uri=queue_rows/queue_rows.sql"`
	Metrics         response.Metrics `parameter:",kind=output,in=metrics"`
}

type QueueRowView struct {
	Id               string     `sqlx:"id"`
	UserId           string     `sqlx:"user_id"`
	ConversationId   *string    `sqlx:"conversation_id"`
	TurnId           *string    `sqlx:"turn_id"`
	MessageId        *string    `sqlx:"message_id"`
	ToolName         string     `sqlx:"tool_name"`
	Title            *string    `sqlx:"title"`
	Arguments        []byte     `sqlx:"arguments"`
	Metadata         *[]byte    `sqlx:"metadata"`
	Status           string     `sqlx:"status"`
	Decision         *string    `sqlx:"decision"`
	ApprovedByUserId *string    `sqlx:"approved_by_user_id"`
	ApprovedAt       *time.Time `sqlx:"approved_at"`
	ExecutedAt       *time.Time `sqlx:"executed_at"`
	ErrorMessage     *string    `sqlx:"error_message"`
	CreatedAt        time.Time  `sqlx:"created_at"`
	UpdatedAt        *time.Time `sqlx:"updated_at"`
}

var QueueRowsPathURI = "/v1/api/agently/toolapprovalqueue/list"

func DefineQueueRowsComponent(ctx context.Context, srv *datly.Service) error {
	aComponent, err := repository.NewComponent(
		contract.NewPath("GET", QueueRowsPathURI),
		repository.WithResource(srv.Resource()),
		repository.WithContract(
			reflect.TypeOf(QueueRowsInput{}),
			reflect.TypeOf(QueueRowsOutput{}),
			&QueueRowsFS,
			view.WithConnectorRef("agently"),
		),
	)
	if err != nil {
		return fmt.Errorf("failed to create QueueRows component: %w", err)
	}
	if err := datlycompat.AddComponent(ctx, srv, aComponent); err != nil {
		return fmt.Errorf("failed to add QueueRows component: %w", err)
	}
	return nil
}

func (i *QueueRowsInput) EmbedFS() *embed.FS { return &QueueRowsFS }
