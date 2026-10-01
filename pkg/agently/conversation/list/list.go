package conversation

import (
	"github.com/viant/xdatly/response"

	"time"
)

// Public data shapes retained for Go caller and JSON compatibility.

type ConversationRowsInput struct {
	AgentId          string
	ParentId         string
	ParentTurnId     string
	ExcludeChildren  bool
	ExcludeScheduled bool
	ScheduleId       string
	ScheduleRunId    string
	Query            string
	StatusFilter     string
	CreatedSince     time.Time
	CreatedBefore    time.Time
	CursorBefore     string
	CursorAfter      string
	DefaultPredicate string
	Has              *ConversationRowsInputHas `setMarker:"true" format:"-" sqlx:"-" diff:"-" json:"-"`
}

type ConversationRowsInputHas struct {
	AgentId          bool
	ParentId         bool
	ParentTurnId     bool
	ExcludeChildren  bool
	ExcludeScheduled bool
	ScheduleId       bool
	ScheduleRunId    bool
	Query            bool
	StatusFilter     bool
	CreatedSince     bool
	CreatedBefore    bool
	CursorBefore     bool
	CursorAfter      bool
	DefaultPredicate bool
}

type ConversationRowsOutput struct {
	response.Status `json:",omitempty"`
	Data            []*ConversationRowsView
	Metrics         response.Metrics
}

type ConversationRowsView struct {
	LastTurnId               *string    `sqlx:"last_turn_id"`
	Stage                    string     `sqlx:"stage"`
	Id                       string     `sqlx:"id"`
	Summary                  *string    `sqlx:"summary"`
	LastActivity             *time.Time `sqlx:"last_activity"`
	UsageInputTokens         *int       `sqlx:"usage_input_tokens"`
	UsageOutputTokens        *int       `sqlx:"usage_output_tokens"`
	UsageEmbeddingTokens     *int       `sqlx:"usage_embedding_tokens"`
	CreatedAt                time.Time  `sqlx:"created_at"`
	UpdatedAt                *time.Time `sqlx:"updated_at"`
	CreatedByUserId          *string    `sqlx:"created_by_user_id"`
	AgentId                  *string    `sqlx:"agent_id"`
	DefaultModelProvider     *string    `sqlx:"default_model_provider"`
	DefaultModel             *string    `sqlx:"default_model"`
	DefaultModelParams       *string    `sqlx:"default_model_params"`
	Title                    *string    `sqlx:"title"`
	ConversationParentId     *string    `sqlx:"conversation_parent_id"`
	ConversationParentTurnId *string    `sqlx:"conversation_parent_turn_id"`
	Metadata                 *string    `sqlx:"metadata"`
	Visibility               string     `sqlx:"visibility"`
	Shareable                int        `sqlx:"shareable"`
	Status                   *string    `sqlx:"status"`
	Scheduled                *int       `sqlx:"scheduled"`
	ScheduleId               *string    `sqlx:"schedule_id"`
	ScheduleRunId            *string    `sqlx:"schedule_run_id"`
	ScheduleKind             *string    `sqlx:"schedule_kind"`
	ScheduleTimezone         *string    `sqlx:"schedule_timezone"`
	ScheduleCronExpr         *string    `sqlx:"schedule_cron_expr"`
	ExternalTaskRef          *string    `sqlx:"external_task_ref"`
}

var ConversationRowsPathURI = "/v1/api/agently/conversation/list/list"
