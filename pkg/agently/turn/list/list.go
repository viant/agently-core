package turn

import (
	"github.com/viant/xdatly/response"

	"time"
)

// Public data shapes retained for Go caller and JSON compatibility.

type TurnRowsInput struct {
	ConversationID string
	TurnId         string
	Statuses       []string
	CreatedSince   time.Time
	CreatedBefore  time.Time
	CursorBefore   string
	CursorAfter    string
	Has            *TurnRowsInputHas `setMarker:"true" format:"-" sqlx:"-" diff:"-" json:"-"`
}

type TurnRowsInputHas struct {
	ConversationID bool
	TurnId         bool
	Statuses       bool
	CreatedSince   bool
	CreatedBefore  bool
	CursorBefore   bool
	CursorAfter    bool
}

type TurnRowsOutput struct {
	response.Status `json:",omitempty"`
	Data            []*TurnRowsView
	Metrics         response.Metrics
}

type TurnRowsView struct {
	Id                    string    `sqlx:"id"`
	ConversationId        string    `sqlx:"conversation_id"`
	CreatedAt             time.Time `sqlx:"created_at"`
	QueueSeq              *int      `sqlx:"queue_seq"`
	Status                string    `sqlx:"status"`
	ErrorMessage          *string   `sqlx:"error_message"`
	StartedByMessageId    *string   `sqlx:"started_by_message_id"`
	RetryOf               *string   `sqlx:"retry_of"`
	AgentIdUsed           *string   `sqlx:"agent_id_used"`
	AgentConfigUsedId     *string   `sqlx:"agent_config_used_id"`
	ModelOverrideProvider *string   `sqlx:"model_override_provider"`
	ModelOverride         *string   `sqlx:"model_override"`
	ModelParamsOverride   *string   `sqlx:"model_params_override"`
	RunId                 *string   `sqlx:"run_id"`
}

var TurnRowsPathURI = "/v1/api/agently/turn/list/list"
