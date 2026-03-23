package run

import (
	"context"
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
	core.RegisterType("run", "RunListInput", reflect.TypeOf(RunListInput{}), checksum.GeneratedTime)
	core.RegisterType("run", "RunListOutput", reflect.TypeOf(RunListOutput{}), checksum.GeneratedTime)
}

type RunListInput struct {
	ScheduledKind    string           `parameter:",kind=const,in=value" predicate:"equal,group=0,t,conversation_kind" value:"scheduled"`
	ScheduleId       string           `parameter:",kind=query,in=scheduleId" predicate:"equal,group=0,t,schedule_id"`
	RunStatus        string           `parameter:",kind=query,in=status" predicate:"equal,group=0,t,status"`
	DefaultPredicate string           `parameter:",kind=const,in=value" predicate:"handler,group=0,*run.Filter" value:"0"`
	Has              *RunListInputHas `setMarker:"true" format:"-" sqlx:"-" diff:"-" json:"-"`
}

type RunListInputHas struct {
	ScheduledKind    bool
	ScheduleId       bool
	RunStatus        bool
	DefaultPredicate bool
}

type RunListOutput struct {
	response.Status `parameter:",kind=output,in=status" json:",omitempty"`
	Data            []*RunView       `parameter:",kind=output,in=view" view:"run,batch=10000,relationalConcurrency=1" sql:"uri=run/run.sql"`
	Metrics         response.Metrics `parameter:",kind=output,in=metrics"`
}

var RunListPathURI = "/v1/api/agently/scheduler/run"

func DefineRunListComponent(ctx context.Context, srv *datly.Service) error {
	aComponent, err := repository.NewComponent(
		contract.NewPath("GET", RunListPathURI),
		repository.WithResource(srv.Resource()),
		repository.WithContract(
			reflect.TypeOf(RunListInput{}),
			reflect.TypeOf(RunListOutput{}), &RunFS, view.WithConnectorRef("agently")))

	if err != nil {
		return fmt.Errorf("failed to create RunList component: %w", err)
	}
	if err := srv.AddComponent(ctx, aComponent); err != nil {
		return fmt.Errorf("failed to add RunList component: %w", err)
	}
	return nil
}
