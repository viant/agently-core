package run

import (
	"time"
)

// RunDueInput is an internal scheduler-only read contract that intentionally
// omits the default visibility predicate handler used by RunInput.
type RunDueInput struct {
	Id              string          `parameter:",kind=path,in=id" predicate:"equal,group=0,t,schedule_id"`
	Since           string          `parameter:",kind=query,in=since" predicate:"expr,group=1,created_at >= (SELECT created_at FROM turn WHERE id = ?)"`
	ScheduledFor    time.Time       `parameter:",kind=query,in=scheduled_for" predicate:"equal,group=0,t,scheduled_for"`
	ExcludeStatuses []string        `parameter:",kind=query,in=status" predicate:"not_in,group=0,t,status"`
	Has             *RunDueInputHas `setMarker:"true" format:"-" sqlx:"-" diff:"-" json:"-"`
}

type RunDueInputHas struct {
	Id              bool
	Since           bool
	ScheduledFor    bool
	ExcludeStatuses bool
}

const RunPathRunDueURI = "/v1/api/agently/scheduler/run/_internal/rundue/{id}"
