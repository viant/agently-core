package toolapprovalqueue

import (
	"context"
	"embed"
	"fmt"
	"reflect"
	"time"

	"github.com/viant/datly"
	"github.com/viant/datly/repository"
	"github.com/viant/datly/repository/contract"
	"github.com/viant/datly/view"
	"github.com/viant/xdatly/handler/response"
	"github.com/viant/xdatly/types/core"
	"github.com/viant/xdatly/types/custom/dependency/checksum"
)

// Source of truth: dql/agently/toolapprovalqueue/outcome.dql
func init() {
	core.RegisterType("toolapprovalqueue", "OutcomeRowsInput", reflect.TypeOf(OutcomeRowsInput{}), checksum.GeneratedTime)
	core.RegisterType("toolapprovalqueue", "OutcomeRowsOutput", reflect.TypeOf(OutcomeRowsOutput{}), checksum.GeneratedTime)
}

//go:embed outcome_rows/*.sql
var OutcomeRowsFS embed.FS

type OutcomeRowsInput struct {
	UserId         string               `parameter:",kind=query,in=userId" predicate:"equal,group=0,o,user_id"`
	ConversationId string               `parameter:",kind=query,in=conversationId" predicate:"equal,group=0,o,conversation_id"`
	Since          time.Time            `parameter:",kind=query,in=since" predicate:"expr,group=0,o.transition_at > ?"`
	Until          time.Time            `parameter:",kind=query,in=until" predicate:"expr,group=0,o.transition_at <= ?"`
	Has            *OutcomeRowsInputHas `setMarker:"true" format:"-" sqlx:"-" diff:"-" json:"-"`
}

type OutcomeRowsInputHas struct {
	UserId         bool
	ConversationId bool
	Since          bool
	Until          bool
}

type OutcomeRowsOutput struct {
	response.Status `parameter:",kind=output,in=apiStatus" json:",omitempty"`
	Data            []*OutcomeRowView `parameter:",kind=output,in=view" view:"outcome_rows,batch=1000,relationalConcurrency=1" sql:"uri=outcome_rows/outcome_rows.sql"`
	Metrics         response.Metrics  `parameter:",kind=output,in=metrics"`
}

type OutcomeRowView struct {
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
	ExpiresAt        *time.Time `sqlx:"expires_at"`
	TimedOutAt       *time.Time `sqlx:"timed_out_at"`
	ApprovedByUserId *string    `sqlx:"approved_by_user_id"`
	ApprovedAt       *time.Time `sqlx:"approved_at"`
	ExecutedAt       *time.Time `sqlx:"executed_at"`
	ErrorMessage     *string    `sqlx:"error_message"`
	CreatedAt        time.Time  `sqlx:"created_at"`
	UpdatedAt        *time.Time `sqlx:"updated_at"`
	TransitionAt     *string    `sqlx:"transition_at"`
}

var OutcomeRowsPathURI = "/v1/api/agently/toolapprovalqueue/outcome/outcome"

func DefineOutcomeRowsComponent(ctx context.Context, srv *datly.Service) error {
	aComponent, err := repository.NewComponent(
		contract.NewPath("GET", OutcomeRowsPathURI),
		repository.WithResource(srv.Resource()),
		repository.WithContract(
			reflect.TypeOf(OutcomeRowsInput{}),
			reflect.TypeOf(OutcomeRowsOutput{}),
			&OutcomeRowsFS,
			view.WithConnectorRef("agently"),
		),
	)
	if err != nil {
		return fmt.Errorf("failed to create OutcomeRows component: %w", err)
	}
	if err := srv.AddComponent(ctx, aComponent); err != nil {
		return fmt.Errorf("failed to add OutcomeRows component: %w", err)
	}
	return nil
}

func (i *OutcomeRowsInput) EmbedFS() *embed.FS { return &OutcomeRowsFS }
