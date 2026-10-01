package run

import (
	"github.com/viant/xdatly/response"

	"time"
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

type RunStepsView struct {
	StepType       string     `sqlx:"step_type"`
	RunId          *string    `sqlx:"run_id"`
	ConversationId *string    `sqlx:"conversation_id"`
	Iteration      *int       `sqlx:"iteration"`
	MessageId      string     `sqlx:"message_id"`
	Name           string     `sqlx:"name"`
	Status         string     `sqlx:"status"`
	StartedAt      *time.Time `sqlx:"started_at"`
	CompletedAt    *time.Time `sqlx:"completed_at"`
	LatencyMs      *int       `sqlx:"latency_ms"`
	ErrorMessage   *string    `sqlx:"error_message"`
}

var RunStepsPathURI = "/v1/api/agently/run/steps/steps"
