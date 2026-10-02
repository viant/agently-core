package reportcontext

import "time"

// Record is the CAS-protected active report pointer for one owner and
// conversation.
type Record struct {
	OwnerID           string    `json:"ownerId"`
	ConversationID    string    `json:"conversationId"`
	ActiveReportRunID string    `json:"activeReportRunId"`
	Revision          int64     `json:"revision"`
	ActivationSource  string    `json:"activationSource,omitempty"`
	ActorID           string    `json:"actorId,omitempty"`
	UpdatedAt         time.Time `json:"updatedAt"`
}
