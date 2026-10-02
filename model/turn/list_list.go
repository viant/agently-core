package turn

import (
	read "github.com/viant/agently-core/internal/datly/turn/read"
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

// TurnRowsView uses the canonical DQL reader shape.
type TurnRowsView = read.TurnRowsView

var TurnRowsPathURI = "/v1/api/agently/turn/list/list"
