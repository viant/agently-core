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
	core.RegisterType("run", "RunListInput", reflect.TypeOf(RunListInput{}), checksum.GeneratedTime)
	core.RegisterType("run", "RunListOutput", reflect.TypeOf(RunListOutput{}), checksum.GeneratedTime)
}

//go:embed list/*.sql
var RunListFS embed.FS

type RunListInput struct {
	EffectiveUserID string           `parameter:",kind=query,in=effectiveUserId"`
	Limit           int              `parameter:",kind=query,in=limit"`
	Offset          int              `parameter:",kind=query,in=offset"`
	ScheduleId      string           `parameter:",kind=query,in=scheduleId" predicate:"expr,group=0,t.schedule_id = ?"`
	RunStatus       string           `parameter:",kind=query,in=status" predicate:"equal,group=0,t,status"`
	Has             *RunListInputHas `setMarker:"true" format:"-" sqlx:"-" diff:"-" json:"-"`
}

type RunListInputHas struct {
	EffectiveUserID bool
	Limit           bool
	Offset          bool
	ScheduleId      bool
	RunStatus       bool
}

type RunListOutput struct {
	response.Status `parameter:",kind=output,in=status" json:",omitempty"`
	Data            []*RunView       `parameter:",kind=output,in=view" view:"run,batch=10000,relationalConcurrency=1" sql:"uri=list/list.sql"`
	Metrics         response.Metrics `parameter:",kind=output,in=metrics"`
}

var RunListPathURI = "/v1/api/agently/scheduler/run"

func DefineRunListComponent(ctx context.Context, srv *datly.Service) error {
	aComponent, err := repository.NewComponent(
		contract.NewPath("GET", RunListPathURI),
		repository.WithResource(srv.Resource()),
		repository.WithContract(
			reflect.TypeOf(RunListInput{}),
			reflect.TypeOf(RunListOutput{}), &RunListFS, view.WithConnectorRef("agently")))

	if err != nil {
		return fmt.Errorf("failed to create RunList component: %w", err)
	}
	if err := srv.AddComponent(ctx, aComponent); err != nil {
		return fmt.Errorf("failed to add RunList component: %w", err)
	}
	return nil
}

func (i *RunListInput) EmbedFS() *embed.FS {
	return &RunListFS
}
