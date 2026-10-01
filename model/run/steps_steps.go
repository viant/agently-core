package run

import (
	read "github.com/viant/agently-core/internal/datly/runsteps/read"
	"github.com/viant/xdatly/response"
)

// Public data shapes retained for Go caller and JSON compatibility.

type RunStepsInput struct {
	RunID        string
	Iteration    int
	StepTypes    []string
	CursorBefore string
	CursorAfter  string
	Has          *RunStepsInputHas `setMarker:"true" format:"-" sqlx:"-" diff:"-" json:"-"`
}

type RunStepsInputHas struct {
	RunID        bool
	Iteration    bool
	StepTypes    bool
	CursorBefore bool
	CursorAfter  bool
}

type RunStepsOutput struct {
	response.Status `json:",omitempty"`
	Data            []*RunStepsView
	Metrics         response.Metrics
}

type RunStepsView = read.RunStepsView

var RunStepsPathURI = "/v1/api/agently/run/steps/steps"
