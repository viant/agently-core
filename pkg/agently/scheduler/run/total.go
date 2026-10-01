package run

import (
	"github.com/viant/xdatly/response"
)

type RunTotalInput struct {
	Since           string            `parameter:",kind=query,in=since" predicate:"expr,group=1,t.created_at >= (SELECT created_at FROM turn WHERE id = ?)"`
	EffectiveUserID string            `parameter:",kind=query,in=effectiveUserId"`
	ScheduleId      string            `parameter:",kind=query,in=scheduleId" predicate:"expr,group=0,t.schedule_id = ?"`
	RunStatus       string            `parameter:",kind=query,in=status" predicate:"expr,group=0,LOWER(CASE WHEN t.status IS NULL THEN '' ELSE t.status END) LIKE LOWER(?)"`
	ConversationId  string            `parameter:",kind=query,in=conversationId" predicate:"expr,group=0,LOWER(CASE WHEN t.conversation_id IS NULL THEN '' ELSE t.conversation_id END) LIKE LOWER(?)"`
	ErrorMessage    string            `parameter:",kind=query,in=errorMessage" predicate:"expr,group=0,LOWER(CASE WHEN t.error_message IS NULL THEN '' ELSE t.error_message END) LIKE LOWER(?)"`
	Has             *RunTotalInputHas `setMarker:"true" format:"-" sqlx:"-" diff:"-" json:"-"`
}

type RunTotalInputHas struct {
	Since           bool
	EffectiveUserID bool
	ScheduleId      bool
	RunStatus       bool
	ConversationId  bool
	ErrorMessage    bool
}

type RunTotalOutput struct {
	response.Status `parameter:",kind=output,in=status" json:",omitempty"`
	Data            []*RunTotalView  `parameter:",kind=output,in=view" view:"run_total,batch=1,relationalConcurrency=1"`
	Metrics         response.Metrics `parameter:",kind=output,in=metrics"`
}

type RunTotalView struct {
	RecordCount int `sqlx:"record_count"`
}

var RunTotalPathURI = "/v1/api/agently/scheduler/run/total"
