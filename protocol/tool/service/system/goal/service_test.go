package goal

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/store/data"
	aggoal "github.com/viant/agently-core/pkg/agently/goal"
	aggoalwrite "github.com/viant/agently-core/pkg/agently/goal/write"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
)

type stubStore struct {
	goal   *aggoal.GoalView
	convID string
}

func (s *stubStore) GetGoal(_ context.Context, conversationID string, _ *aggoal.GoalInput, _ ...data.Option) (*aggoal.GoalView, error) {
	if s.goal == nil || conversationID != s.convID {
		return nil, nil
	}
	cp := *s.goal
	return &cp, nil
}

func (s *stubStore) PatchGoals(_ context.Context, rows []*aggoalwrite.MutableGoalView) ([]*aggoalwrite.MutableGoalView, error) {
	for _, row := range rows {
		if row == nil || row.Has == nil {
			continue
		}
		if s.goal == nil {
			s.goal = &aggoal.GoalView{Id: row.Id}
		}
		if row.Has.Id {
			s.goal.Id = row.Id
		}
		if row.Has.ConversationID && row.ConversationID != nil {
			s.goal.ConversationID = *row.ConversationID
			s.convID = *row.ConversationID
		}
		if row.Has.Objective && row.Objective != nil {
			s.goal.Objective = *row.Objective
		}
		if row.Has.Status && row.Status != nil {
			s.goal.Status = *row.Status
		}
		if row.Has.StatusReason {
			s.goal.StatusReason = row.StatusReason
		}
		if row.Has.PauseReason {
			s.goal.PauseReason = row.PauseReason
		}
		if row.Has.ControllerSpec {
			s.goal.ControllerSpec = row.ControllerSpec
		}
		if row.Has.TokenBudget {
			s.goal.TokenBudget = row.TokenBudget
		}
		if row.Has.TokensUsed && row.TokensUsed != nil {
			s.goal.TokensUsed = *row.TokensUsed
		}
		if row.Has.TimeUsedSeconds && row.TimeUsedSeconds != nil {
			s.goal.TimeUsedSeconds = *row.TimeUsedSeconds
		}
	}
	return rows, nil
}

func (s *stubStore) DeleteGoals(_ context.Context, ids ...string) error {
	for _, id := range ids {
		if s.goal != nil && s.goal.Id == id {
			s.goal = nil
		}
	}
	return nil
}

func TestService_CreateGetUpdate(t *testing.T) {
	store := &stubStore{}
	svc := New(store)
	ctx := runtimerequestctx.WithConversationID(context.Background(), "conv-1")

	createOut := &CreateOutput{}
	err := svc.create(ctx, &CreateInput{
		Objective:   "reduce latency",
		TokenBudget: ptrInt64(5000),
	}, createOut)
	require.NoError(t, err)
	require.NotNil(t, createOut.Goal)
	require.Equal(t, "goal-conv-1", createOut.Goal.ID)
	require.Equal(t, "reduce latency", createOut.Goal.Objective)
	require.Equal(t, StatusActive, createOut.Goal.Status)
	require.EqualValues(t, 5000, *createOut.Goal.TokenBudget)

	getOut := &GetOutput{}
	require.NoError(t, svc.get(ctx, &GetInput{}, getOut))
	require.NotNil(t, getOut.Goal)
	require.Equal(t, "reduce latency", getOut.Goal.Objective)

	updateOut := &UpdateOutput{}
	require.NoError(t, svc.update(ctx, &UpdateInput{Status: "complete", Reason: "work finished successfully"}, updateOut))
	require.NotNil(t, updateOut.Goal)
	require.Equal(t, StatusComplete, updateOut.Goal.Status)
	require.NotNil(t, updateOut.Goal.StatusReason)
	require.Equal(t, "work finished successfully", *updateOut.Goal.StatusReason)
}

func TestService_CreateFailsWhenGoalExists(t *testing.T) {
	store := &stubStore{
		convID: "conv-1",
		goal: &aggoal.GoalView{
			Id:             "goal-conv-1",
			ConversationID: "conv-1",
			Objective:      "existing",
			Status:         StatusActive,
		},
	}
	svc := New(store)
	ctx := runtimerequestctx.WithConversationID(context.Background(), "conv-1")

	err := svc.create(ctx, &CreateInput{Objective: "new"}, &CreateOutput{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "goal already exists")
}

func TestService_UpdateRejectsNonTerminalModelStatuses(t *testing.T) {
	store := &stubStore{
		convID: "conv-1",
		goal: &aggoal.GoalView{
			Id:             "goal-conv-1",
			ConversationID: "conv-1",
			Objective:      "existing",
			Status:         StatusActive,
		},
	}
	svc := New(store)
	ctx := runtimerequestctx.WithConversationID(context.Background(), "conv-1")

	err := svc.update(ctx, &UpdateInput{Status: "paused", Reason: "not allowed"}, &UpdateOutput{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "status must be one of")
}

func TestService_UpdateRequiresReason(t *testing.T) {
	store := &stubStore{
		convID: "conv-1",
		goal: &aggoal.GoalView{
			Id:             "goal-conv-1",
			ConversationID: "conv-1",
			Objective:      "existing",
			Status:         StatusActive,
		},
	}
	svc := New(store)
	ctx := runtimerequestctx.WithConversationID(context.Background(), "conv-1")

	err := svc.update(ctx, &UpdateInput{Status: "blocked"}, &UpdateOutput{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "reason is required")
}

func TestService_RequiresConversationContext(t *testing.T) {
	svc := New(&stubStore{})
	err := svc.get(context.Background(), &GetInput{}, &GetOutput{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "conversation context is required")
}

func TestService_PauseResumeClear(t *testing.T) {
	store := &stubStore{
		convID: "conv-1",
		goal: &aggoal.GoalView{
			Id:             "goal-conv-1",
			ConversationID: "conv-1",
			Objective:      "existing",
			Status:         StatusActive,
		},
	}
	svc := New(store)
	ctx := runtimerequestctx.WithConversationID(context.Background(), "conv-1")

	pauseOut := &PauseOutput{}
	require.NoError(t, svc.pause(ctx, &PauseInput{Reason: "waiting for review"}, pauseOut))
	require.NotNil(t, pauseOut.Goal)
	require.Equal(t, "paused", pauseOut.Goal.Status)
	require.NotNil(t, pauseOut.Goal.PauseReason)
	require.Equal(t, "waiting for review", *pauseOut.Goal.PauseReason)

	resumeOut := &ResumeOutput{}
	require.NoError(t, svc.resume(ctx, &ResumeInput{}, resumeOut))
	require.NotNil(t, resumeOut.Goal)
	require.Equal(t, StatusActive, resumeOut.Goal.Status)
	require.Nil(t, resumeOut.Goal.PauseReason)

	clearOut := &ClearOutput{}
	require.NoError(t, svc.clear(ctx, &ClearInput{}, clearOut))
	require.True(t, clearOut.Cleared)

	getOut := &GetOutput{}
	require.NoError(t, svc.get(ctx, &GetInput{}, getOut))
	require.Nil(t, getOut.Goal)
}

func ptrInt64(v int64) *int64 { return &v }
