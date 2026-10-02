package schedule

type ScheduleListInput struct {
	DefaultPredicate string `parameter:",kind=const,in=value" predicate:"handler,group=0,*schedule.Filter" value:"0"`
}

const SchedulePathListURI = "/v1/api/agently/scheduler/"
