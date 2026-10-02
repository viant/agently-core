package schedule

import (
	read "github.com/viant/agently-core/internal/datly/schedule/read"
	"github.com/viant/xdatly/response"
)

// Public facade inputs and envelopes are separate from the generated row view.

type ScheduleInput struct {
	Id               string            `parameter:",kind=path,in=id" predicate:"equal,group=0,t,id"`
	DefaultPredicate string            `parameter:",kind=const,in=value" predicate:"handler,group=0,*schedule.Filter" value:"0"`
	Has              *ScheduleInputHas `setMarker:"true" format:"-" sqlx:"-" diff:"-" json:"-"`
}

type ScheduleInputHas struct {
	Id               bool
	DefaultPredicate bool
}

type ScheduleOutput struct {
	response.Status `parameter:",kind=output,in=status" json:",omitempty"`
	Data            []*ScheduleView  `parameter:",kind=output,in=view" view:"schedule,batch=10000,relationalConcurrency=1"`
	Metrics         response.Metrics `parameter:",kind=output,in=metrics"`
}

type ScheduleView = read.ScheduleView

var SchedulePathURI = "/v1/api/agently/scheduler/schedule/{id}"
