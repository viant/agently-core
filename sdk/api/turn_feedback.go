package api

import "time"

// TurnFeedback is the owner's optional assessment of a turn's final answer.
// Zero and nulls are explicit so clients can also apply a cleared assessment.
type TurnFeedback struct {
	Rating    int        `json:"rating"`
	Comment   *string    `json:"comment"`
	UpdatedAt *time.Time `json:"updatedAt"`
}

// SetTurnFeedbackInput replaces the rating and optional comment together.
type SetTurnFeedbackInput struct {
	ConversationID string  `json:"-"`
	TurnID         string  `json:"-"`
	Rating         int     `json:"rating"`
	Comment        *string `json:"comment"`
}
