package turn

import (
	"github.com/viant/xdatly/response"

	"time"
)

// Public data shapes retained for Go caller and JSON compatibility.

type QueuedTurnInput struct {
	ConversationID string
	Has            *QueuedTurnInputHas `setMarker:"true" format:"-" sqlx:"-" diff:"-" json:"-"`
}

type QueuedTurnInputHas struct {
	ConversationID bool
}

type QueuedTurnOutput struct {
	response.Status `json:",omitempty"`
	Data            []*QueuedTurnView
	Metrics         response.Metrics
}

type QueuedTurnView struct {
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

var QueuedTurnPathURI = "/v1/api/agently/turn/nextQueued/nextQueued"
