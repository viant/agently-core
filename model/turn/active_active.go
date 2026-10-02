package turn

import (
	read "github.com/viant/agently-core/internal/datly/turn/read"
	"github.com/viant/xdatly/response"
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

// ActiveTurnsView uses the canonical DQL reader shape.
type ActiveTurnsView = read.TurnRowsView

var ActiveTurnsPathURI = "/v1/api/agently/turn/active/active"
