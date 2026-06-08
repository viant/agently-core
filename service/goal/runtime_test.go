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
