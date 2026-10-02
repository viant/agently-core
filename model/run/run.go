package run

import (
	read "github.com/viant/agently-core/internal/datly/run/read"
	"github.com/viant/xdatly/response"
)

// Public data shapes retained for Go caller and JSON compatibility.

type RunRowsInput struct {
	Id               string
	TurnId           string
	ConversationId   string
	ScheduleId       string
	WorkerId         string
	RunStatus        string
	ExcludeStatuses  []string
	DefaultPredicate string
	Has              *RunRowsInputHas `setMarker:"true" format:"-" sqlx:"-" diff:"-" json:"-"`
}

type RunRowsInputHas struct {
	Id               bool
	TurnId           bool
	ConversationId   bool
	ScheduleId       bool
	WorkerId         bool
	RunStatus        bool
	ExcludeStatuses  bool
	DefaultPredicate bool
}

type RunRowsOutput struct {
	response.Status `json:",omitempty"`
	Data            []*RunRowsView
	Metrics         response.Metrics
}

// RunRowsView uses the canonical DQL reader shape.
type RunRowsView = read.RunRowsView

var RunRowsPathURI = "/v1/api/agently/run/{id}"
