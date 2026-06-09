package goal

import (
	"context"
	"strings"
	"time"

	"github.com/viant/agently-core/runtime/usage"
)

// Runtime bridges the durable goal store and the pure controller policy.
// It owns the side effects that happen around a completed turn:
// usage accounting, state transitions, and continuation decisions.
type Runtime struct {
	store        Store
	controller   *Controller
	now          func() time.Time
	onDeactivate func(ctx context.Context, conversationID, goalID string)
}

type projectedControllerState struct {
	autonomousTurnsUsed   int64
	consecutiveNoProgress int64
	fingerprint           string
	ready                 bool
}

type AfterTurnInput struct {
	ConversationID string
	TurnStatus     string
	RequestTime    time.Time
	Usage          *usage.Aggregator

	TurnRunning        bool
	QueuedUserTurns    int
	PendingElicitation bool
	PendingApproval    bool
	PendingAsync       bool
	UsageLimited       bool

	ConsecutiveNoProgress int
	AutonomousTurnsUsed   int
	Continuation          *ContinuationHint
	ProgressFingerprint   string
}

func NewRuntime(store Store) *Runtime {
	return &Runtime{
		store:      store,
		controller: NewController(),
		now:        time.Now,
	}
}

func (r *Runtime) SetDeactivateHook(fn func(ctx context.Context, conversationID, goalID string)) {
	if r == nil {
		return
	}
	r.onDeactivate = fn
}

func (r *Runtime) AfterTurn(ctx context.Context, in *AfterTurnInput) (Action, *Goal, error) {
	if r == nil || r.store == nil || in == nil {
		return Action{Kind: ActionNone, Reason: "goal runtime not configured"}, nil, nil
	}
	conversationID := strings.TrimSpace(in.ConversationID)
	if conversationID == "" {
		return Action{Kind: ActionNone, Reason: "missing conversation id"}, nil, nil
	}
	goal, err := r.store.Current(ctx, conversationID)
	if err != nil || goal == nil {
		return Action{Kind: ActionNone, Reason: "no goal"}, goal, err
	}

	// Account turn usage before policy evaluation so budget checks use the
	// newest totals.
	if strings.EqualFold(strings.TrimSpace(in.TurnStatus), "succeeded") {
		prompt, completion, embed, _ := inUsageTotals(in.Usage)
		tokenDelta := int64(prompt + completion + embed)
		if tokenDelta > 0 || !in.RequestTime.IsZero() {
			elapsed := int64(0)
			if !in.RequestTime.IsZero() {
				elapsed = int64(r.now().Sub(in.RequestTime).Seconds())
				if elapsed < 0 {
					elapsed = 0
				}
			}
			if err := r.store.RecordUsage(ctx, goal.ID, goal.TokensUsed+tokenDelta, goal.TimeUsedSeconds+elapsed); err != nil {
				return Action{}, goal, err
			}
			goal, err = r.store.Current(ctx, conversationID)
			if err != nil {
				return Action{}, nil, err
			}
			if goal == nil {
				return Action{Kind: ActionNone, Reason: "goal missing after usage update"}, nil, nil
			}
		}
	}

	continuation := in.Continuation
	if continuation == nil && strings.EqualFold(strings.TrimSpace(in.TurnStatus), "succeeded") && goal.Autonomous() {
		continuation = defaultContinuationHint(goal)
	}
	projected := projectControllerState(goal, continuation, strings.TrimSpace(in.ProgressFingerprint))

	action := r.controller.Evaluate(&Snapshot{
		Goal:                  goal,
		TurnRunning:           in.TurnRunning,
		QueuedUserTurns:       in.QueuedUserTurns,
		PendingElicitation:    in.PendingElicitation,
		PendingApproval:       in.PendingApproval,
		PendingAsync:          in.PendingAsync,
		UsageLimited:          in.UsageLimited,
		ConsecutiveNoProgress: projectedOrFallback(projected.ready, projected.consecutiveNoProgress, int64(in.ConsecutiveNoProgress)),
		AutonomousTurnsUsed:   projectedOrFallback(projected.ready, projected.autonomousTurnsUsed, int64(in.AutonomousTurnsUsed)),
		Continuation:          continuation,
	})

	switch action.Kind {
	case ActionPauseGoal:
		if projected.ready {
			if err := r.store.UpdateControllerState(ctx, goal.ID, projected.autonomousTurnsUsed, projected.consecutiveNoProgress, projected.fingerprint); err != nil {
				return action, goal, err
			}
		}
		if err := r.store.Pause(ctx, goal.ID, action.PauseReason); err != nil {
			return action, goal, err
		}
		if r.onDeactivate != nil {
			r.onDeactivate(ctx, conversationID, goal.ID)
		}
		goal, _ = r.store.Current(ctx, conversationID)
	case ActionBlockGoal, ActionCompleteGoal, ActionBudgetLimited, ActionUsageLimited:
		if projected.ready && (action.Kind == ActionBlockGoal || action.Kind == ActionCompleteGoal) {
			if err := r.store.UpdateControllerState(ctx, goal.ID, projected.autonomousTurnsUsed, projected.consecutiveNoProgress, projected.fingerprint); err != nil {
				return action, goal, err
			}
		}
		if err := r.store.Transition(ctx, goal.ID, action.Status, action.Reason); err != nil {
			return action, goal, err
		}
		if r.onDeactivate != nil {
			r.onDeactivate(ctx, conversationID, goal.ID)
		}
		goal, _ = r.store.Current(ctx, conversationID)
	case ActionQueueTurn:
		if projected.ready {
			if err := r.store.UpdateControllerState(ctx, goal.ID, projected.autonomousTurnsUsed, projected.consecutiveNoProgress, projected.fingerprint); err != nil {
				return action, goal, err
			}
			goal, _ = r.store.Current(ctx, conversationID)
		}
	case ActionScheduleWakeup:
		if projected.ready {
			if err := r.store.UpdateControllerState(ctx, goal.ID, projected.autonomousTurnsUsed, projected.consecutiveNoProgress, projected.fingerprint); err != nil {
				return action, goal, err
			}
			goal, _ = r.store.Current(ctx, conversationID)
		}
	}
	return action, goal, nil
}

func inUsageTotals(agg *usage.Aggregator) (prompt, completion, embed, cached int) {
	if agg == nil {
		return 0, 0, 0, 0
	}
	return agg.Totals()
}

func defaultContinuationHint(goal *Goal) *ContinuationHint {
	if goal == nil {
		return nil
	}
	objective := strings.TrimSpace(goal.Objective)
	if objective == "" {
		return nil
	}
	preview := "Continue goal: " + objective
	return &ContinuationHint{
		Reason:  "continue active goal",
		Preview: preview,
		Payload: "Continue working toward the active goal.\nGoal: " + objective + "\nUse the existing conversation context and take the next concrete step.",
	}
}

func projectControllerState(goal *Goal, continuation *ContinuationHint, progressFingerprint string) projectedControllerState {
	if goal == nil || continuation == nil || !goal.Autonomous() {
		return projectedControllerState{}
	}
	fingerprint := strings.TrimSpace(progressFingerprint)
	if fingerprint == "" {
		fingerprint = ContinuationFingerprint(continuation)
	}
	consecutive := int64(0)
	if goal.LastContinuationFingerprint != "" && goal.LastContinuationFingerprint == fingerprint {
		consecutive = goal.ConsecutiveNoProgress + 1
	}
	return projectedControllerState{
		autonomousTurnsUsed:   goal.AutonomousTurnsUsed + 1,
		consecutiveNoProgress: consecutive,
		fingerprint:           fingerprint,
		ready:                 true,
	}
}

func projectedOrFallback(hasProjected bool, projected int64, fallback int64) int {
	if hasProjected {
		return int(projected)
	}
	return int(fallback)
}
