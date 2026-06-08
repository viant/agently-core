package goal

import "fmt"

// Status is the durable lifecycle state of a goal. The string values match the
// `status` column in the goal table exactly.
type Status string

const (
	StatusActive        Status = "active"
	StatusPaused        Status = "paused"
	StatusBlocked       Status = "blocked"
	StatusComplete      Status = "complete"
	StatusBudgetLimited Status = "budget_limited"
	StatusUsageLimited  Status = "usage_limited"
)

// ParseStatus converts a persisted status string into a typed Status. It
// returns an error for any unrecognised value rather than guessing a default.
func ParseStatus(value string) (Status, error) {
	switch Status(value) {
	case StatusActive, StatusPaused, StatusBlocked, StatusComplete, StatusBudgetLimited, StatusUsageLimited:
		return Status(value), nil
	default:
		return "", fmt.Errorf("unknown goal status %q", value)
	}
}

// IsActive reports whether the goal is eligible for autonomous continuation.
func (s Status) IsActive() bool { return s == StatusActive }

// IsTerminal reports whether the goal has reached a state from which the
// controller must never continue.
func (s Status) IsTerminal() bool {
	switch s {
	case StatusComplete, StatusBlocked, StatusBudgetLimited, StatusUsageLimited:
		return true
	default:
		return false
	}
}

// PauseReason explains why an otherwise-valid goal stopped continuing. It is
// persisted in the dedicated pause_reason column, separate from status_reason.
type PauseReason string

const (
	PauseReasonUserRequested         PauseReason = "user_requested"
	PauseReasonUserInterrupt         PauseReason = "user_interrupt"
	PauseReasonUserNewTurn           PauseReason = "user_new_turn"
	PauseReasonHumanReviewCheckpoint PauseReason = "human_review_checkpoint"
	PauseReasonModeChange            PauseReason = "mode_change"
	PauseReasonSupervisorPolicy      PauseReason = "supervisor_policy"
)
