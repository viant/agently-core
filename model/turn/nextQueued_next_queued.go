package turn

import (
	read "github.com/viant/agently-core/internal/datly/turn/read"
	"github.com/viant/xdatly/response"
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

// QueuedTurnView uses the canonical DQL reader shape.
type QueuedTurnView = read.TurnRowsView

var QueuedTurnPathURI = "/v1/api/agently/turn/nextQueued/nextQueued"
