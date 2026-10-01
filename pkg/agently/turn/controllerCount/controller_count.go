package turn

import (
	"github.com/viant/xdatly/response"
)

// ControllerCount returns the number of controller-owned (autonomous) turns
// created for a conversation. It mirrors the QueuedTotal count component but
// filters on origin instead of status. It is used to derive
// AutonomousTurnsUsed for the goal controller snapshot.

type ControllerTotalInput struct {
	ConversationID string
	Has            *ControllerTotalInputHas `setMarker:"true" format:"-" sqlx:"-" diff:"-" json:"-"`
}

type ControllerTotalInputHas struct {
	ConversationID bool
}

type ControllerTotalOutput struct {
	response.Status `json:",omitempty"`
	Data            []*ControllerTotalView
	Metrics         response.Metrics
}

type ControllerTotalView struct {
	ControllerCount int `sqlx:"controller_count"`
}

var ControllerTotalPathURI = "/v1/api/agently/turn/controllerCount/controllerCount"
