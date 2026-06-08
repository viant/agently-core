// Package goal implements the autonomous-system runtime above the durable goal
// persistence (pkg/agently/goal) and the system/goal model tool. It composes
// three deliberately separate concerns:
//
//  1. Domain model — the runtime-owned Goal, Status, and ControllerSpec types.
//  2. Store boundary — maps the Datly read/write types to and from the domain
//     model so no generated type leaks upward.
//  3. Controller — a pure, deterministic policy that decides whether and how an
//     active goal continues.
//
// It never infers state from transcript text and never substitutes a guessed
// default for missing configuration: a goal without a controller spec is simply
// not autonomous.
package goal

import "time"

// Goal is the runtime-owned view of a durable conversation objective. It is
// distinct from the persistence GoalView and from the tool-facing projection;
// the store boundary translates between them.
type Goal struct {
	ID              string
	ConversationID  string
	Objective       string
	Status          Status
	StatusReason    string
	PauseReason     PauseReason
	Controller      *ControllerSpec
	TokenBudget     *int64
	TokensUsed      int64
	TimeUsedSeconds int64
	CreatedAt       time.Time
	UpdatedAt       *time.Time
}

// BudgetExceeded reports whether a token budget is set and has been reached.
func (g *Goal) BudgetExceeded() bool {
	return g != nil && g.TokenBudget != nil && g.TokensUsed >= *g.TokenBudget
}

// Autonomous reports whether the goal opted into idle-time continuation.
func (g *Goal) Autonomous() bool {
	return g != nil && g.Controller != nil && g.Controller.ContinueMode == ContinueModeIdleOnly
}
