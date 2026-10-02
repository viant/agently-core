package run

import (
	read "github.com/viant/agently-core/internal/datly/run/read"
	"github.com/viant/xdatly/response"
)

// Public data shapes retained for Go caller and JSON compatibility.

type ActiveRunsInput struct {
	TurnId         string
	ConversationId string
	Has            *ActiveRunsInputHas `setMarker:"true" format:"-" sqlx:"-" diff:"-" json:"-"`
}

type ActiveRunsInputHas struct {
	TurnId         bool
	ConversationId bool
}

type ActiveRunsOutput struct {
	response.Status `json:",omitempty"`
	Data            []*ActiveRunsView
	Metrics         response.Metrics
}

// ActiveRunsView uses the canonical DQL reader shape.
type ActiveRunsView = read.RunRowsView

var ActiveRunsPathURI = "/v1/api/agently/run/active/active"
