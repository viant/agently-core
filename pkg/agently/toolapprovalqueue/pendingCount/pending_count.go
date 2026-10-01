package toolapprovalqueue

import (
	"github.com/viant/xdatly/response"
)

// PendingCount returns the number of pending tool approvals for a conversation.
// It mirrors the turn QueuedTotal count component and is used to derive the
// PendingApproval guard for the goal controller snapshot.

type PendingTotalInput struct {
	ConversationID string
	Has            *PendingTotalInputHas `setMarker:"true" format:"-" sqlx:"-" diff:"-" json:"-"`
}

type PendingTotalInputHas struct {
	ConversationID bool
}

type PendingTotalOutput struct {
	response.Status `json:",omitempty"`
	Data            []*PendingTotalView
	Metrics         response.Metrics
}

type PendingTotalView struct {
	PendingCount int `sqlx:"pending_count"`
}

var PendingTotalPathURI = "/v1/api/agently/toolapprovalqueue/pendingCount/pendingCount"
