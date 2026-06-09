package goal

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func intPtr(v int) *int       { return &v }
func int64Ptr(v int64) *int64 { return &v }

// idleSpec returns a controller spec that permits idle continuation with the
// given optional guards.
func idleSpec() *ControllerSpec {
	return &ControllerSpec{
		ContinueMode:     ContinueModeIdleOnly,
		OnTurnFinished:   TurnPolicyEvaluate,
		OnAsyncCompleted: AsyncPolicyEvaluate,
	}
}

func activeGoal(spec *ControllerSpec) *Goal {
	return &Goal{ID: "goal-c1", ConversationID: "c1", Status: StatusActive, Controller: spec}
}

func hint() *ContinuationHint {
	return &ContinuationHint{Reason: "next step", Preview: "continue", Payload: "context"}
}

func TestController_Evaluate(t *testing.T) {
	controller := NewController()

	testCases := []struct {
		name     string
		snapshot *Snapshot
		expect   ActionKind
	}{
		{
			name:     "nil snapshot",
			snapshot: nil,
			expect:   ActionNone,
		},
		{
			name:     "no goal",
			snapshot: &Snapshot{},
			expect:   ActionNone,
		},
		{
			name:     "goal paused",
			snapshot: &Snapshot{Goal: &Goal{Status: StatusPaused, Controller: idleSpec()}, Continuation: hint()},
			expect:   ActionNone,
		},
		{
			name:     "goal complete",
			snapshot: &Snapshot{Goal: &Goal{Status: StatusComplete, Controller: idleSpec()}, Continuation: hint()},
			expect:   ActionNone,
		},
		{
			name: "budget exhausted takes priority over idle state",
			snapshot: &Snapshot{
				Goal:         &Goal{Status: StatusActive, Controller: idleSpec(), TokenBudget: int64Ptr(100), TokensUsed: 100},
				TurnRunning:  true,
				Continuation: hint(),
			},
			expect: ActionBudgetLimited,
		},
		{
			name: "usage limited",
			snapshot: &Snapshot{
				Goal:         activeGoal(idleSpec()),
				UsageLimited: true,
				Continuation: hint(),
			},
			expect: ActionUsageLimited,
		},
		{
			name:     "turn running",
			snapshot: &Snapshot{Goal: activeGoal(idleSpec()), TurnRunning: true, Continuation: hint()},
			expect:   ActionNone,
		},
		{
			name:     "user work queued",
			snapshot: &Snapshot{Goal: activeGoal(idleSpec()), QueuedUserTurns: 1, Continuation: hint()},
			expect:   ActionNone,
		},
		{
			name:     "elicitation pending",
			snapshot: &Snapshot{Goal: activeGoal(idleSpec()), PendingElicitation: true, Continuation: hint()},
			expect:   ActionNone,
		},
		{
			name:     "approval pending",
			snapshot: &Snapshot{Goal: activeGoal(idleSpec()), PendingApproval: true, Continuation: hint()},
			expect:   ActionNone,
		},
		{
			name:     "async pending",
			snapshot: &Snapshot{Goal: activeGoal(idleSpec()), PendingAsync: true, Continuation: hint()},
			expect:   ActionNone,
		},
		{
			name:     "no controller spec is not autonomous",
			snapshot: &Snapshot{Goal: activeGoal(nil), Continuation: hint()},
			expect:   ActionNone,
		},
		{
			name:     "manual-only mode does not continue",
			snapshot: &Snapshot{Goal: activeGoal(&ControllerSpec{ContinueMode: ContinueModeManualOnly, OnTurnFinished: TurnPolicyEvaluate, OnAsyncCompleted: AsyncPolicyEvaluate}), Continuation: hint()},
			expect:   ActionNone,
		},
		{
			name: "no-progress guard blocks",
			snapshot: &Snapshot{
				Goal: activeGoal(&ControllerSpec{
					ContinueMode:             ContinueModeIdleOnly,
					OnTurnFinished:           TurnPolicyEvaluate,
					OnAsyncCompleted:         AsyncPolicyEvaluate,
					MaxConsecutiveNoProgress: intPtr(2),
				}),
				ConsecutiveNoProgress: 2,
				Continuation:          hint(),
			},
			expect: ActionBlockGoal,
		},
		{
			name: "autonomous turn limit pauses",
			snapshot: &Snapshot{
				Goal: activeGoal(&ControllerSpec{
					ContinueMode:       ContinueModeIdleOnly,
					OnTurnFinished:     TurnPolicyEvaluate,
					OnAsyncCompleted:   AsyncPolicyEvaluate,
					MaxAutonomousTurns: intPtr(3),
				}),
				AutonomousTurnsUsed: 3,
				Continuation:        hint(),
			},
			expect: ActionPauseGoal,
		},
		{
			name:     "no continuation signal",
			snapshot: &Snapshot{Goal: activeGoal(idleSpec()), Continuation: nil},
			expect:   ActionNone,
		},
		{
			name: "turn policy wait suppresses continuation",
			snapshot: &Snapshot{
				Goal:         activeGoal(&ControllerSpec{ContinueMode: ContinueModeIdleOnly, OnTurnFinished: TurnPolicyWait, OnAsyncCompleted: AsyncPolicyEvaluate}),
				Continuation: hint(),
			},
			expect: ActionNone,
		},
		{
			name:     "idle active goal with signal queues a turn",
			snapshot: &Snapshot{Goal: activeGoal(idleSpec()), Continuation: hint()},
			expect:   ActionQueueTurn,
		},
		{
			name: "wake delay schedules a wakeup instead of queueing immediately",
			snapshot: &Snapshot{
				Goal: activeGoal(&ControllerSpec{
					ContinueMode:     ContinueModeIdleOnly,
					OnTurnFinished:   TurnPolicyEvaluate,
					OnAsyncCompleted: AsyncPolicyEvaluate,
					WakeDelaySeconds: intPtr(90),
				}),
				Continuation: hint(),
			},
			expect: ActionScheduleWakeup,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			action := controller.Evaluate(tc.snapshot)
			require.Equal(t, tc.expect, action.Kind, "reason: %s", action.Reason)
		})
	}
}

func TestController_QueueTurnCarriesContinuation(t *testing.T) {
	h := hint()
	action := NewController().Evaluate(&Snapshot{Goal: activeGoal(idleSpec()), Continuation: h})
	require.Equal(t, ActionQueueTurn, action.Kind)
	require.Same(t, h, action.Continuation)
}

func TestController_ScheduleWakeupCarriesContinuationAndDelay(t *testing.T) {
	h := hint()
	action := NewController().Evaluate(&Snapshot{
		Goal: activeGoal(&ControllerSpec{
			ContinueMode:     ContinueModeIdleOnly,
			OnTurnFinished:   TurnPolicyEvaluate,
			OnAsyncCompleted: AsyncPolicyEvaluate,
			WakeDelaySeconds: intPtr(120),
		}),
		Continuation: h,
	})
	require.Equal(t, ActionScheduleWakeup, action.Kind)
	require.Same(t, h, action.Continuation)
	require.Equal(t, 120, action.WakeDelaySeconds)
}

func TestController_TransitionActionsCarryStatus(t *testing.T) {
	controller := NewController()

	budget := controller.Evaluate(&Snapshot{Goal: &Goal{Status: StatusActive, Controller: idleSpec(), TokenBudget: int64Ptr(10), TokensUsed: 10}})
	require.Equal(t, ActionBudgetLimited, budget.Kind)
	require.Equal(t, StatusBudgetLimited, budget.Status)

	usage := controller.Evaluate(&Snapshot{Goal: activeGoal(idleSpec()), UsageLimited: true})
	require.Equal(t, ActionUsageLimited, usage.Kind)
	require.Equal(t, StatusUsageLimited, usage.Status)

	pause := controller.Evaluate(&Snapshot{
		Goal:                activeGoal(&ControllerSpec{ContinueMode: ContinueModeIdleOnly, OnTurnFinished: TurnPolicyEvaluate, OnAsyncCompleted: AsyncPolicyEvaluate, MaxAutonomousTurns: intPtr(1)}),
		AutonomousTurnsUsed: 1,
		Continuation:        hint(),
	})
	require.Equal(t, ActionPauseGoal, pause.Kind)
	require.Equal(t, StatusPaused, pause.Status)
	require.Equal(t, PauseReasonHumanReviewCheckpoint, pause.PauseReason)
}
