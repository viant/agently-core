package message

import (
	"github.com/viant/xdatly/response"
)

// ElicitationPendingCount returns the number of unresolved elicitation request
// messages for a conversation. An elicitation request is persisted with
// status='pending' and flips to a terminal status when resolved, so a simple
// status filter yields the pending count. It is used to derive the
// PendingElicitation guard for the goal controller snapshot.

type ElicitationPendingInput struct {
	ConversationID string
	Has            *ElicitationPendingInputHas `setMarker:"true" format:"-" sqlx:"-" diff:"-" json:"-"`
}

type ElicitationPendingInputHas struct {
	ConversationID bool
}

type ElicitationPendingOutput struct {
	response.Status `json:",omitempty"`
	Data            []*ElicitationPendingView
	Metrics         response.Metrics
}

type ElicitationPendingView struct {
	PendingCount int `sqlx:"pending_count"`
}

var ElicitationPendingPathURI = "/v1/api/agently/message/elicitationCount/elicitationCount"
