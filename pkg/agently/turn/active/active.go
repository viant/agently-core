package turn

import (
	"github.com/viant/xdatly/response"

	"time"
)

// Public data shapes retained for Go caller and JSON compatibility.

type ActiveTurnsInput struct {
	ConversationID string
	Has            *ActiveTurnsInputHas `setMarker:"true" format:"-" sqlx:"-" diff:"-" json:"-"`
}

type ActiveTurnsInputHas struct {
	ConversationID bool
}

type ActiveTurnsOutput struct {
	response.Status `json:",omitempty"`
	Data            []*ActiveTurnsView
	Metrics         response.Metrics
}

type ActiveTurnsView struct {
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

var ActiveTurnsPathURI = "/v1/api/agently/turn/active/active"
