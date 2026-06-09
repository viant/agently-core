package goal

// ContinuationHint is an explicit, runtime-supplied next-step signal. The
// controller continues an active goal only when such a hint is present; it
// never infers a next action from free-text transcript output.
type ContinuationHint struct {
	// Reason is the human-readable justification for continuing, surfaced to
	// the user (e.g. on the queued turn).
	Reason string
	// Preview is the visible queue preview text for the scheduled turn.
	Preview string
	// Payload is the hidden controller context handed to the continuation
	// turn as internal input. It is not rendered as a user message.
	Payload string
}

// Snapshot is the complete, side-effect-free input to a controller decision.
// Everything the policy needs is captured here; Evaluate performs no IO.
type Snapshot struct {
	Goal *Goal

	// Conversation activity. Continuation requires a fully idle conversation.
	TurnRunning        bool
	QueuedUserTurns    int
	PendingElicitation bool
	PendingApproval    bool
	PendingAsync       bool

	// Externally detected provider/runtime usage limit. The controller reacts
	// to it but does not detect it.
	UsageLimited bool

	// Progress guards, accounted by the runtime across autonomous turns.
	ConsecutiveNoProgress int
	AutonomousTurnsUsed   int

	// Continuation is the explicit next-step signal, or nil when none exists.
	Continuation *ContinuationHint
}

// ActionKind enumerates the single decisions the controller can emit.
type ActionKind string

const (
	ActionNone           ActionKind = "none"
	ActionQueueTurn      ActionKind = "queue_turn"
	ActionPauseGoal      ActionKind = "pause_goal"
	ActionBlockGoal      ActionKind = "block_goal"
	ActionCompleteGoal   ActionKind = "complete_goal"
	ActionBudgetLimited  ActionKind = "budget_limited"
	ActionUsageLimited   ActionKind = "usage_limited"
	ActionScheduleWakeup ActionKind = "schedule_wakeup"
)

// Action is the controller's single explicit decision.
type Action struct {
	Kind   ActionKind
	Reason string

	// Status is the target status for a lifecycle-transition decision. It is
	// empty for ActionNone and ActionQueueTurn.
	Status Status
	// PauseReason accompanies ActionPauseGoal.
	PauseReason PauseReason
	// Continuation is set only for ActionQueueTurn and carries the hint that
	// justified continuation.
	Continuation *ContinuationHint
	// WakeDelaySeconds is set only for ActionScheduleWakeup.
	WakeDelaySeconds int
}

// Controller is the autonomy policy engine. It is stateless; all inputs arrive
// through the Snapshot.
type Controller struct{}

// NewController returns a controller.
func NewController() *Controller { return &Controller{} }

// Evaluate maps a snapshot to exactly one decision. It is pure and
// deterministic: the same snapshot always yields the same action.
//
// Order of reasoning:
//  1. no goal or non-active goal → no action;
//  2. system-owned hard limits (budget, usage) → terminal transition;
//  3. conversation not idle, or user work pending → wait;
//  4. goal not configured for idle continuation → no action;
//  5. configured stall / autonomous-turn guards → block or pause;
//  6. no explicit continuation signal, or turn policy is wait → no action;
//  7. otherwise → queue one controller-owned continuation turn.
func (c *Controller) Evaluate(s *Snapshot) Action {
	if s == nil || s.Goal == nil {
		return Action{Kind: ActionNone, Reason: "no goal"}
	}
	g := s.Goal
	if !g.Status.IsActive() {
		return Action{Kind: ActionNone, Reason: "goal is not active"}
	}

	// System-owned hard limits are evaluated first so an exhausted goal stops
	// regardless of conversation activity.
	if g.BudgetExceeded() {
		return Action{Kind: ActionBudgetLimited, Status: StatusBudgetLimited, Reason: "token budget exhausted"}
	}
	if s.UsageLimited {
		return Action{Kind: ActionUsageLimited, Status: StatusUsageLimited, Reason: "provider or runtime usage limit reached"}
	}

	// Idle predicate. Never act while the conversation is busy or while
	// user-owned work is pending.
	if s.TurnRunning {
		return Action{Kind: ActionNone, Reason: "a turn is currently running"}
	}
	if s.QueuedUserTurns > 0 {
		return Action{Kind: ActionNone, Reason: "user work is queued"}
	}
	if s.PendingElicitation {
		return Action{Kind: ActionNone, Reason: "an elicitation is pending"}
	}
	if s.PendingApproval {
		return Action{Kind: ActionNone, Reason: "an approval is pending"}
	}
	if s.PendingAsync {
		return Action{Kind: ActionNone, Reason: "async work is still active"}
	}

	// Autonomy must be explicitly enabled by the goal's controller spec.
	if !g.Autonomous() {
		return Action{Kind: ActionNone, Reason: "autonomous continuation is not enabled"}
	}
	spec := g.Controller

	// Configured guards. Each only trips when its limit is set.
	if spec.MaxConsecutiveNoProgress != nil && s.ConsecutiveNoProgress >= *spec.MaxConsecutiveNoProgress {
		return Action{Kind: ActionBlockGoal, Status: StatusBlocked, Reason: "no progress across consecutive autonomous turns"}
	}
	if spec.MaxAutonomousTurns != nil && s.AutonomousTurnsUsed >= *spec.MaxAutonomousTurns {
		return Action{
			Kind:        ActionPauseGoal,
			Status:      StatusPaused,
			PauseReason: PauseReasonHumanReviewCheckpoint,
			Reason:      "autonomous turn limit reached",
		}
	}

	// An explicit next-step signal is required; the policy never invents one.
	if s.Continuation == nil {
		return Action{Kind: ActionNone, Reason: "no explicit continuation signal"}
	}
	if spec.OnTurnFinished == TurnPolicyWait {
		return Action{Kind: ActionNone, Reason: "turn policy is wait"}
	}
	if spec.WakeDelaySeconds != nil && *spec.WakeDelaySeconds > 0 {
		return Action{
			Kind:             ActionScheduleWakeup,
			Reason:           "delay active goal continuation",
			Continuation:     s.Continuation,
			WakeDelaySeconds: *spec.WakeDelaySeconds,
		}
	}

	return Action{Kind: ActionQueueTurn, Reason: "continue active goal", Continuation: s.Continuation}
}
