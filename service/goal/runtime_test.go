package goal

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/runtime/usage"
)

type runtimeStore struct {
	current *Goal
}

func (s *runtimeStore) Current(_ context.Context, _ string) (*Goal, error) {
	if s.current == nil {
		return nil, nil
	}
	cp := *s.current
	return &cp, nil
}

func (s *runtimeStore) RecordUsage(_ context.Context, goalID string, tokensUsed, timeUsedSeconds int64) error {
	if s.current != nil && s.current.ID == goalID {
		s.current.TokensUsed = tokensUsed
		s.current.TimeUsedSeconds = timeUsedSeconds
	}
	return nil
}

func (s *runtimeStore) Transition(_ context.Context, goalID string, status Status, reason string) error {
	if s.current != nil && s.current.ID == goalID {
		s.current.Status = status
		s.current.StatusReason = reason
	}
	return nil
}

func (s *runtimeStore) Pause(_ context.Context, goalID string, reason PauseReason) error {
	if s.current != nil && s.current.ID == goalID {
		s.current.Status = StatusPaused
		s.current.PauseReason = reason
		s.current.StatusReason = ""
	}
	return nil
}

func (s *runtimeStore) UpdateControllerState(_ context.Context, goalID string, autonomousTurnsUsed, consecutiveNoProgress int64, fingerprint string) error {
	if s.current != nil && s.current.ID == goalID {
		s.current.AutonomousTurnsUsed = autonomousTurnsUsed
		s.current.ConsecutiveNoProgress = consecutiveNoProgress
		s.current.LastContinuationFingerprint = fingerprint
	}
	return nil
}

func TestRuntime_AfterTurnQueuesContinuationAndAccountsUsage(t *testing.T) {
	store := &runtimeStore{
		current: &Goal{
			ID:             "goal-1",
			ConversationID: "conv-1",
			Objective:      "finish the refactor",
			Status:         StatusActive,
			Controller:     idleSpec(),
		},
	}
	rt := NewRuntime(store)
	rt.now = func() time.Time { return time.Unix(200, 0) }
	agg := &usage.Aggregator{}
	agg.Add("model", 10, 5, 0, 0)

	action, goal, err := rt.AfterTurn(context.Background(), &AfterTurnInput{
		ConversationID: "conv-1",
		TurnStatus:     "succeeded",
		RequestTime:    time.Unix(140, 0),
		Usage:          agg,
	})
	require.NoError(t, err)
	require.Equal(t, ActionQueueTurn, action.Kind)
	require.NotNil(t, action.Continuation)
	require.Equal(t, int64(15), goal.TokensUsed)
	require.Equal(t, int64(60), goal.TimeUsedSeconds)
}

func TestRuntime_AfterTurnTransitionsOnBudgetLimit(t *testing.T) {
	store := &runtimeStore{
		current: &Goal{
			ID:             "goal-1",
			ConversationID: "conv-1",
			Objective:      "finish the refactor",
			Status:         StatusActive,
			Controller:     idleSpec(),
			TokenBudget:    int64Ptr(10),
			TokensUsed:     9,
		},
	}
	rt := NewRuntime(store)
	agg := &usage.Aggregator{}
	agg.Add("model", 1, 0, 0, 0)

	action, goal, err := rt.AfterTurn(context.Background(), &AfterTurnInput{
		ConversationID: "conv-1",
		TurnStatus:     "succeeded",
		Usage:          agg,
	})
	require.NoError(t, err)
	require.Equal(t, ActionBudgetLimited, action.Kind)
	require.Equal(t, StatusBudgetLimited, goal.Status)
}

func TestRuntime_AfterTurnNoQueueWhenQueuedUserWorkExists(t *testing.T) {
	store := &runtimeStore{
		current: &Goal{
			ID:             "goal-1",
			ConversationID: "conv-1",
			Objective:      "finish the refactor",
			Status:         StatusActive,
			Controller:     idleSpec(),
		},
	}
	rt := NewRuntime(store)

	action, _, err := rt.AfterTurn(context.Background(), &AfterTurnInput{
		ConversationID:  "conv-1",
		TurnStatus:      "succeeded",
		QueuedUserTurns: 1,
	})
	require.NoError(t, err)
	require.Equal(t, ActionNone, action.Kind)
}

func TestRuntime_AfterTurnPersistsProjectedControllerStateOnQueue(t *testing.T) {
	store := &runtimeStore{
		current: &Goal{
			ID:             "goal-1",
			ConversationID: "conv-1",
			Objective:      "finish the refactor",
			Status:         StatusActive,
			Controller:     idleSpec(),
		},
	}
	rt := NewRuntime(store)

	action, goal, err := rt.AfterTurn(context.Background(), &AfterTurnInput{
		ConversationID: "conv-1",
		TurnStatus:     "succeeded",
	})
	require.NoError(t, err)
	require.Equal(t, ActionQueueTurn, action.Kind)
	require.Equal(t, int64(1), goal.AutonomousTurnsUsed)
	require.Equal(t, int64(0), goal.ConsecutiveNoProgress)
	require.NotEmpty(t, goal.LastContinuationFingerprint)
}

func TestRuntime_AfterTurnSchedulesWakeupAndPersistsProjectedControllerState(t *testing.T) {
	store := &runtimeStore{
		current: &Goal{
			ID:             "goal-1",
			ConversationID: "conv-1",
			Objective:      "finish the refactor",
			Status:         StatusActive,
			Controller: &ControllerSpec{
				ContinueMode:     ContinueModeIdleOnly,
				OnTurnFinished:   TurnPolicyEvaluate,
				OnAsyncCompleted: AsyncPolicyEvaluate,
				WakeDelaySeconds: intPtr(90),
			},
		},
	}
	rt := NewRuntime(store)

	action, goal, err := rt.AfterTurn(context.Background(), &AfterTurnInput{
		ConversationID: "conv-1",
		TurnStatus:     "succeeded",
	})
	require.NoError(t, err)
	require.Equal(t, ActionScheduleWakeup, action.Kind)
	require.Equal(t, 90, action.WakeDelaySeconds)
	require.Equal(t, int64(1), goal.AutonomousTurnsUsed)
	require.Equal(t, int64(0), goal.ConsecutiveNoProgress)
	require.NotEmpty(t, goal.LastContinuationFingerprint)
}

func TestRuntime_AfterTurnCallsDeactivateHookOnPauseAndBlock(t *testing.T) {
	store := &runtimeStore{
		current: &Goal{
			ID:             "goal-1",
			ConversationID: "conv-1",
			Objective:      "finish the refactor",
			Status:         StatusActive,
			Controller: &ControllerSpec{
				ContinueMode:       ContinueModeIdleOnly,
				OnTurnFinished:     TurnPolicyEvaluate,
				OnAsyncCompleted:   AsyncPolicyEvaluate,
				MaxAutonomousTurns: intPtr(1),
			},
		},
	}
	rt := NewRuntime(store)
	var calls []string
	rt.SetDeactivateHook(func(_ context.Context, conversationID, goalID string) {
		calls = append(calls, conversationID+":"+goalID)
	})

	action, _, err := rt.AfterTurn(context.Background(), &AfterTurnInput{
		ConversationID:      "conv-1",
		TurnStatus:          "succeeded",
		AutonomousTurnsUsed: 1,
	})
	require.NoError(t, err)
	require.Equal(t, ActionPauseGoal, action.Kind)
	require.Equal(t, []string{"conv-1:goal-1"}, calls)

	store.current.Status = StatusActive
	repeat := &ContinuationHint{Reason: "continue", Preview: "continue", Payload: "continue"}
	store.current.Controller = &ControllerSpec{
		ContinueMode:             ContinueModeIdleOnly,
		OnTurnFinished:           TurnPolicyEvaluate,
		OnAsyncCompleted:         AsyncPolicyEvaluate,
		MaxConsecutiveNoProgress: intPtr(1),
	}
	store.current.ConsecutiveNoProgress = 1
	store.current.LastContinuationFingerprint = ContinuationFingerprint(repeat)
	action, _, err = rt.AfterTurn(context.Background(), &AfterTurnInput{
		ConversationID: "conv-1",
		TurnStatus:     "succeeded",
		Continuation:   repeat,
	})
	require.NoError(t, err)
	require.Equal(t, ActionBlockGoal, action.Kind)
	require.Equal(t, []string{"conv-1:goal-1", "conv-1:goal-1"}, calls)
}

func TestRuntime_AfterTurnIncrementsConsecutiveNoProgressForRepeatedContinuation(t *testing.T) {
	hint := &ContinuationHint{
		Reason:  "continue active goal",
		Preview: "Continue goal: finish the refactor",
		Payload: "Continue working toward the active goal.\nGoal: finish the refactor",
	}
	store := &runtimeStore{
		current: &Goal{
			ID:             "goal-1",
			ConversationID: "conv-1",
			Objective:      "finish the refactor",
			Status:         StatusActive,
			Controller: &ControllerSpec{
				ContinueMode:             ContinueModeIdleOnly,
				OnTurnFinished:           TurnPolicyEvaluate,
				OnAsyncCompleted:         AsyncPolicyEvaluate,
				MaxConsecutiveNoProgress: intPtr(2),
			},
			AutonomousTurnsUsed:         1,
			ConsecutiveNoProgress:       1,
			LastContinuationFingerprint: ContinuationFingerprint(hint),
		},
	}
	rt := NewRuntime(store)

	action, goal, err := rt.AfterTurn(context.Background(), &AfterTurnInput{
		ConversationID: "conv-1",
		TurnStatus:     "succeeded",
		Continuation:   hint,
	})
	require.NoError(t, err)
	require.Equal(t, ActionBlockGoal, action.Kind)
	require.Equal(t, StatusBlocked, goal.Status)
	require.Equal(t, int64(2), goal.ConsecutiveNoProgress)
}
