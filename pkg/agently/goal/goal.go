package goal

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

func init() {
	core.RegisterType("goal", "GoalInput", reflect.TypeOf(GoalInput{}), checksum.GeneratedTime)
	core.RegisterType("goal", "GoalOutput", reflect.TypeOf(GoalOutput{}), checksum.GeneratedTime)
}

// Source of truth: dql/agently/goal/goal.dql

//go:embed goal/*.sql
var GoalFS embed.FS

type GoalInput struct {
	ConversationID string        `parameter:",kind=path,in=conversationId" predicate:"equal,group=0,t,conversation_id"`
	Has            *GoalInputHas `setMarker:"true" format:"-" sqlx:"-" diff:"-" json:"-"`
}

type GoalInputHas struct {
	ConversationID bool
}

type GoalOutput struct {
	response.Status `parameter:",kind=output,in=status" json:",omitempty"`
	Data            []*GoalView      `parameter:",kind=output,in=view" view:"goal,batch=1000,relationalConcurrency=1" sql:"uri=goal/goal.sql"`
	Metrics         response.Metrics `parameter:",kind=output,in=metrics"`
}

type GoalView struct {
	Id              string     `sqlx:"id"`
	ConversationID  string     `sqlx:"conversation_id"`
	Objective       string     `sqlx:"objective"`
	Status          string     `sqlx:"status"`
	StatusReason    *string    `sqlx:"status_reason"`
	PauseReason     *string    `sqlx:"pause_reason"`
	ControllerSpec  *string    `sqlx:"controller_spec"`
	TokenBudget     *int64     `sqlx:"token_budget"`
	TokensUsed      int64      `sqlx:"tokens_used"`
	TimeUsedSeconds int64      `sqlx:"time_used_seconds"`
	CreatedAt       time.Time  `sqlx:"created_at"`
	UpdatedAt       *time.Time `sqlx:"updated_at"`
}

var GoalPathURI = "/v1/api/agently/goal/{conversationId}"

func DefineGoalComponent(ctx context.Context, srv *datly.Service) error {
	component, err := repository.NewComponent(
		contract.NewPath("GET", GoalPathURI),
		repository.WithResource(srv.Resource()),
		repository.WithContract(
			reflect.TypeOf(GoalInput{}),
			reflect.TypeOf(GoalOutput{}),
			&GoalFS,
			view.WithConnectorRef("agently"),
		),
	)
	if err != nil {
		return fmt.Errorf("failed to create Goal component: %w", err)
	}
	if err := srv.AddComponent(ctx, component); err != nil {
		return fmt.Errorf("failed to add Goal component: %w", err)
	}
	return nil
}

func (i *GoalInput) EmbedFS() *embed.FS { return &GoalFS }
