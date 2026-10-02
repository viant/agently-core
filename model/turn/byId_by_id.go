package turn

import (
	read "github.com/viant/agently-core/internal/datly/turn/read"
	"github.com/viant/xdatly/response"
)

// Public data shapes retained for Go caller and JSON compatibility.

type TurnLookupInput struct {
	ID             string
	ConversationID string
	Has            *TurnLookupInputHas `setMarker:"true" format:"-" sqlx:"-" diff:"-" json:"-"`
}

type TurnLookupInputHas struct {
	ID             bool
	ConversationID bool
}

type TurnLookupOutput struct {
	response.Status `json:",omitempty"`
	Data            []*TurnLookupView
	Metrics         response.Metrics
}

// TurnLookupView uses the canonical DQL reader shape.
type TurnLookupView = read.TurnRowsView

var TurnLookupPathURI = "/v1/api/agently/turn/byId/byId"
