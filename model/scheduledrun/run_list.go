package scheduledrun

import (
	"github.com/viant/xdatly/response"
)

type RunListInput struct {
	Since           string           `parameter:",kind=query,in=since" predicate:"expr,group=1,t.created_at >= (SELECT created_at FROM turn WHERE id = ?)"`
	EffectiveUserID string           `parameter:",kind=query,in=effectiveUserId"`
	Limit           int              `parameter:",kind=query,in=limit"`
	Offset          int              `parameter:",kind=query,in=offset"`
	ScheduleId      string           `parameter:",kind=query,in=scheduleId" predicate:"expr,group=0,t.schedule_id = ?"`
	RunStatus       string           `parameter:",kind=query,in=status" predicate:"expr,group=0,LOWER(CASE WHEN t.status IS NULL THEN '' ELSE t.status END) LIKE LOWER(?)"`
	ConversationId  string           `parameter:",kind=query,in=conversationId" predicate:"expr,group=0,LOWER(CASE WHEN t.conversation_id IS NULL THEN '' ELSE t.conversation_id END) LIKE LOWER(?)"`
	ErrorMessage    string           `parameter:",kind=query,in=errorMessage" predicate:"expr,group=0,LOWER(CASE WHEN t.error_message IS NULL THEN '' ELSE t.error_message END) LIKE LOWER(?)"`
	Has             *RunListInputHas `setMarker:"true" format:"-" sqlx:"-" diff:"-" json:"-"`
}

type RunListInputHas struct {
	Since           bool
	EffectiveUserID bool
	Limit           bool
	Offset          bool
	ScheduleId      bool
	RunStatus       bool
	ConversationId  bool
	ErrorMessage    bool
}

type RunListOutput struct {
	response.Status `parameter:",kind=output,in=status" json:",omitempty"`
	Data            []*RunView       `parameter:",kind=output,in=view" view:"run,batch=10000,relationalConcurrency=1"`
	Metrics         response.Metrics `parameter:",kind=output,in=metrics"`
}

var RunListPathURI = "/v1/api/agently/scheduler/run"
