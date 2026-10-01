package schedule

import ()

// ScheduleRunDueListInput is used by scheduler internals to list all schedules
// without user-visibility filtering.
type ScheduleRunDueListInput struct{}

const SchedulePathListRunDueURI = "/v1/api/agently/scheduler/schedule/_internal/rundue"
