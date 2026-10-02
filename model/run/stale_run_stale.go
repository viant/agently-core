package run

import (
	read "github.com/viant/agently-core/internal/datly/run/read"
	"github.com/viant/xdatly/response"

	"time"
)

// Public data shapes retained for Go caller and JSON compatibility.

type StaleRunsInput struct {
	HeartbeatBefore    time.Time
	WorkerHost         string
	LeaseExpiredBefore time.Time
	ActivityAfter      time.Time
	ConversationKind   string
	RootInteractive    bool
	Has                *StaleRunsInputHas `setMarker:"true" format:"-" sqlx:"-" diff:"-" json:"-"`
}

type StaleRunsInputHas struct {
	HeartbeatBefore    bool
	WorkerHost         bool
	LeaseExpiredBefore bool
	ActivityAfter      bool
	ConversationKind   bool
	RootInteractive    bool
}

type StaleRunsOutput struct {
	response.Status `json:",omitempty"`
	Data            []*StaleRunsView
	Metrics         response.Metrics
}

// StaleRunsView uses the canonical DQL reader shape.
type StaleRunsView = read.RunRowsView

var StaleRunsPathURI = "/v1/api/agently/run/stale/stale"
