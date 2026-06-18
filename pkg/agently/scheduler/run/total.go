package run

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
	checksum "github.com/viant/xdatly/types/custom/dependency/checksum"
)

func init() {
	core.RegisterType("run", "RunTotalInput", reflect.TypeOf(RunTotalInput{}), checksum.GeneratedTime)
	core.RegisterType("run", "RunTotalOutput", reflect.TypeOf(RunTotalOutput{}), checksum.GeneratedTime)
}

//go:embed total/*.sql
var RunTotalFS embed.FS

type RunTotalInput struct {
	EffectiveUserID string            `parameter:",kind=query,in=effectiveUserId"`
	ScheduleId      string            `parameter:",kind=query,in=scheduleId" predicate:"expr,group=0,t.schedule_id = ?"`
	RunStatus       string            `parameter:",kind=query,in=status" predicate:"expr,group=0,LOWER(CASE WHEN t.status IS NULL THEN '' ELSE t.status END) LIKE '%' || LOWER(?) || '%'"`
	ConversationId  string            `parameter:",kind=query,in=conversationId" predicate:"expr,group=0,LOWER(CASE WHEN t.conversation_id IS NULL THEN '' ELSE t.conversation_id END) LIKE '%' || LOWER(?) || '%'"`
	ErrorMessage    string            `parameter:",kind=query,in=errorMessage" predicate:"expr,group=0,LOWER(CASE WHEN t.error_message IS NULL THEN '' ELSE t.error_message END) LIKE '%' || LOWER(?) || '%'"`
	Has             *RunTotalInputHas `setMarker:"true" format:"-" sqlx:"-" diff:"-" json:"-"`
}

type RunTotalInputHas struct {
	EffectiveUserID bool
	ScheduleId      bool
	RunStatus       bool
	ConversationId  bool
	ErrorMessage    bool
}

type RunTotalOutput struct {
	response.Status `parameter:",kind=output,in=status" json:",omitempty"`
	Data            []*RunTotalView  `parameter:",kind=output,in=view" view:"run_total,batch=1,relationalConcurrency=1" sql:"uri=total/total.sql"`
	Metrics         response.Metrics `parameter:",kind=output,in=metrics"`
}

type RunTotalView struct {
	RecordCount int `sqlx:"record_count"`
}

var RunTotalPathURI = "/v1/api/agently/scheduler/run/total"

func DefineRunTotalComponent(ctx context.Context, srv *datly.Service) error {
	aComponent, err := repository.NewComponent(
		contract.NewPath("GET", RunTotalPathURI),
		repository.WithResource(srv.Resource()),
		repository.WithContract(
			reflect.TypeOf(RunTotalInput{}),
			reflect.TypeOf(RunTotalOutput{}), &RunTotalFS, view.WithConnectorRef("agently")))

	if err != nil {
		return fmt.Errorf("failed to create RunTotal component: %w", err)
	}
	if err := srv.AddComponent(ctx, aComponent); err != nil {
		return fmt.Errorf("failed to add RunTotal component: %w", err)
	}
	return nil
}

func (i *RunTotalInput) EmbedFS() *embed.FS {
	return &RunTotalFS
}
