package toolcall

import (
	"context"
	"embed"
	"fmt"
	"github.com/viant/datly"
	"github.com/viant/datly/repository"
	"github.com/viant/datly/repository/contract"
	"github.com/viant/datly/view"
	"github.com/viant/xdatly/handler/response"
	"github.com/viant/xdatly/types/core"
	"github.com/viant/xdatly/types/custom/dependency/checksum"
	"reflect"
	"time"
)

func init() {
	core.RegisterType("toolcall", "ToolCallsInput", reflect.TypeOf(ToolCallsInput{}), checksum.GeneratedTime)
	core.RegisterType("toolcall", "ToolCallsOutput", reflect.TypeOf(ToolCallsOutput{}), checksum.GeneratedTime)
}

//go:embed tool_calls/*.sql
var ToolCallsFS embed.FS

type ToolCallsInput struct {
	TurnId       string             `parameter:",kind=query,in=turnId" predicate:"equal,group=0,t,turn_id"`
	Statuses     []string           `parameter:",kind=query,in=statuses" predicate:"in,group=0,t,status"`
	CreatedSince time.Time          `parameter:",kind=query,in=createdSince" predicate:"greater_or_equal,group=0,t,started_at"`
	Has          *ToolCallsInputHas `setMarker:"true" format:"-" sqlx:"-" diff:"-" json:"-"`
}

type ToolCallsInputHas struct {
	TurnId       bool
	Statuses     bool
	CreatedSince bool
}

type ToolCallsOutput struct {
	response.Status `parameter:",kind=output,in=status" json:",omitempty"`
	Data            []*ToolCallsView `parameter:",kind=output,in=view" view:"tool_calls,batch=10000,relationalConcurrency=1" sql:"uri=tool_calls/tool_calls.sql"`
	Metrics         response.Metrics `parameter:",kind=output,in=metrics"`
}

type ToolCallsView struct {
	MessageId      string     `sqlx:"message_id"`
	TurnId         *string    `sqlx:"turn_id"`
	ConversationId *string    `sqlx:"conversation_id"`
	OpId           string     `sqlx:"op_id"`
	ToolName       string     `sqlx:"tool_name"`
	Status         string     `sqlx:"status"`
	ErrorMessage   *string    `sqlx:"error_message"`
	StartedAt      *time.Time `sqlx:"started_at"`
	CompletedAt    *time.Time `sqlx:"completed_at"`
	RunId          *string    `sqlx:"run_id"`
}

var ToolCallsPathURI = "/v1/api/agently/toolcall/list/list"

func DefineToolCallsComponent(ctx context.Context, srv *datly.Service) error {
	component, err := repository.NewComponent(
		contract.NewPath("GET", ToolCallsPathURI),
		repository.WithResource(srv.Resource()),
		repository.WithContract(
			reflect.TypeOf(ToolCallsInput{}),
			reflect.TypeOf(ToolCallsOutput{}), &ToolCallsFS, view.WithConnectorRef("agently")))
	if err != nil {
		return fmt.Errorf("failed to create ToolCalls component: %w", err)
	}
	if err := srv.AddComponent(ctx, component); err != nil {
		return fmt.Errorf("failed to add ToolCalls component: %w", err)
	}
	return nil
}

func (i *ToolCallsInput) EmbedFS() *embed.FS {
	return &ToolCallsFS
}
