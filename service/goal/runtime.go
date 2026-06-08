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
	store      Store
	controller *Controller
	now        func() time.Time
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
}

func NewRuntime(store Store) *Runtime {
	return &Runtime{
		store:      store,
		controller: NewController(),
		now:        time.Now,
	}
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

	action := r.controller.Evaluate(&Snapshot{
		Goal:                  goal,
		TurnRunning:           in.TurnRunning,
		QueuedUserTurns:       in.QueuedUserTurns,
		PendingElicitation:    in.PendingElicitation,
		PendingApproval:       in.PendingApproval,
		PendingAsync:          in.PendingAsync,
		UsageLimited:          in.UsageLimited,
		ConsecutiveNoProgress: in.ConsecutiveNoProgress,
		AutonomousTurnsUsed:   in.AutonomousTurnsUsed,
		Continuation:          continuation,
	})

	switch action.Kind {
	case ActionPauseGoal:
		if err := r.store.Pause(ctx, goal.ID, action.PauseReason); err != nil {
			return action, goal, err
		}
		goal, _ = r.store.Current(ctx, conversationID)
	case ActionBlockGoal, ActionCompleteGoal, ActionBudgetLimited, ActionUsageLimited:
		if err := r.store.Transition(ctx, goal.ID, action.Status, action.Reason); err != nil {
			return action, goal, err
		}
		goal, _ = r.store.Current(ctx, conversationID)
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
