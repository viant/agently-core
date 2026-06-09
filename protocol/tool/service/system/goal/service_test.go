package goal

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/store/data"
	aggoal "github.com/viant/agently-core/pkg/agently/goal"
	aggoalwrite "github.com/viant/agently-core/pkg/agently/goal/write"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/service/scheduler"
	"github.com/viant/agently-core/workspace"
)

type stubStore struct {
	current *aggoal.GoalView
	rows    []*aggoalwrite.MutableGoalView
	deleted []string
}

func (s *stubStore) GetGoal(_ context.Context, _ string, _ *aggoal.GoalInput, _ ...data.Option) (*aggoal.GoalView, error) {
	return s.current, nil
}

func (s *stubStore) PatchGoals(_ context.Context, rows []*aggoalwrite.MutableGoalView) ([]*aggoalwrite.MutableGoalView, error) {
	s.rows = append(s.rows, rows...)
	for _, row := range rows {
		if row == nil {
			continue
		}
		if s.current == nil {
			s.current = &aggoal.GoalView{Id: row.Id}
		}
		if row.Id != "" {
			s.current.Id = row.Id
		}
		if row.ConversationID != nil {
			s.current.ConversationID = *row.ConversationID
		}
		if row.Objective != nil {
			s.current.Objective = *row.Objective
		}
		if row.Status != nil {
			s.current.Status = *row.Status
		}
		if row.Has != nil && row.Has.StatusReason && row.StatusReason == nil {
			s.current.StatusReason = nil
		} else if row.StatusReason != nil {
			s.current.StatusReason = row.StatusReason
		}
		if row.Has != nil && row.Has.PauseReason && row.PauseReason == nil {
			s.current.PauseReason = nil
		} else if row.PauseReason != nil {
			s.current.PauseReason = row.PauseReason
		}
		if row.ControllerSpec != nil {
			s.current.ControllerSpec = row.ControllerSpec
		}
		if row.TokenBudget != nil {
			s.current.TokenBudget = row.TokenBudget
		}
	}
	return rows, nil
}

func (s *stubStore) DeleteGoals(_ context.Context, ids ...string) error {
	s.deleted = append(s.deleted, ids...)
	return nil
}

type captureWakeups struct {
	calls [][2]string
}

func (c *captureWakeups) CancelGoalWakeups(_ context.Context, conversationID, goalID string) error {
	c.calls = append(c.calls, [2]string{conversationID, goalID})
	return nil
}

func goalContext() context.Context {
	return runtimerequestctx.WithConversationID(context.Background(), "conv-1")
}

func activeGoal() *aggoal.GoalView {
	return &aggoal.GoalView{Id: "goal-conv-1", ConversationID: "conv-1", Objective: "finish cleanup", Status: "active"}
}

type stubScheduleReader struct {
	wakeup *scheduler.GoalControllerSchedule
}

func (s *stubScheduleReader) CurrentGoalWakeup(_ context.Context, _, _ string) *scheduler.GoalControllerSchedule {
	return s.wakeup
}

func TestService_UpdateCancelsPendingWakeups(t *testing.T) {
	store := &stubStore{current: activeGoal()}
	wakeups := &captureWakeups{}
	svc := New(store)
	svc.SetWakeupCanceler(wakeups)

	out := &UpdateOutput{}
	err := svc.update(goalContext(), &UpdateInput{Status: "blocked", Reason: "no valid path"}, out)
	require.NoError(t, err)
	require.Equal(t, [][2]string{{"conv-1", "goal-conv-1"}}, wakeups.calls)
}

func TestService_PauseResumeAndClearCancelPendingWakeups(t *testing.T) {
	store := &stubStore{current: activeGoal()}
	wakeups := &captureWakeups{}
	svc := New(store)
	svc.SetWakeupCanceler(wakeups)

	require.NoError(t, svc.pause(goalContext(), &PauseInput{Reason: "user paused"}, &PauseOutput{}))
	require.NoError(t, svc.resume(goalContext(), &ResumeInput{}, &ResumeOutput{}))
	require.NoError(t, svc.clear(goalContext(), &ClearInput{}, &ClearOutput{}))

	require.Equal(t,
		[][2]string{
			{"conv-1", "goal-conv-1"},
			{"conv-1", "goal-conv-1"},
			{"conv-1", "goal-conv-1"},
		},
		wakeups.calls,
	)
}

func TestService_GoalOutputsIncludeControllerSchedule(t *testing.T) {
	prevRoot := workspace.Root()
	tempRoot := t.TempDir()
	workspace.SetRoot(tempRoot)
	defer workspace.SetRoot(prevRoot)

	err := os.WriteFile(filepath.Join(tempRoot, "config.yaml"), []byte(`
features:
  goals:
    enabled: true
`), 0o644)
	require.NoError(t, err)

	wakeAt := time.Date(2026, time.June, 8, 18, 30, 0, 0, time.UTC)
	reader := &stubScheduleReader{
		wakeup: &scheduler.GoalControllerSchedule{
			Mode:    "wakeup",
			Preview: scheduleStringPtr("Resume after review"),
			WakeAt:  &wakeAt,
		},
	}

	tests := []struct {
		name  string
		store *stubStore
		run   func(*Service) (*Goal, error)
	}{
		{
			name:  "get",
			store: &stubStore{current: activeGoal()},
			run: func(svc *Service) (*Goal, error) {
				out := &GetOutput{}
				err := svc.get(goalContext(), &GetInput{}, out)
				return out.Goal, err
			},
		},
		{
			name:  "create",
			store: &stubStore{},
			run: func(svc *Service) (*Goal, error) {
				out := &CreateOutput{}
				err := svc.create(goalContext(), &CreateInput{Objective: "finish cleanup"}, out)
				return out.Goal, err
			},
		},
		{
			name:  "update",
			store: &stubStore{current: activeGoal()},
			run: func(svc *Service) (*Goal, error) {
				out := &UpdateOutput{}
				err := svc.update(goalContext(), &UpdateInput{Status: "blocked", Reason: "waiting on review"}, out)
				return out.Goal, err
			},
		},
		{
			name:  "pause",
			store: &stubStore{current: activeGoal()},
			run: func(svc *Service) (*Goal, error) {
				out := &PauseOutput{}
				err := svc.pause(goalContext(), &PauseInput{Reason: "user paused"}, out)
				return out.Goal, err
			},
		},
		{
			name:  "resume",
			store: &stubStore{current: &aggoal.GoalView{Id: "goal-conv-1", ConversationID: "conv-1", Objective: "finish cleanup", Status: "paused"}},
			run: func(svc *Service) (*Goal, error) {
				out := &ResumeOutput{}
				err := svc.resume(goalContext(), &ResumeInput{}, out)
				return out.Goal, err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			svc := New(test.store)
			svc.SetControllerScheduleReader(reader)

			goal, err := test.run(svc)
			require.NoError(t, err)
			require.NotNil(t, goal)
			require.NotNil(t, goal.ControllerSchedule)
			require.Equal(t, "wakeup", goal.ControllerSchedule.Mode)
			require.NotNil(t, goal.ControllerSchedule.Preview)
			require.Equal(t, "Resume after review", *goal.ControllerSchedule.Preview)
			require.NotNil(t, goal.ControllerSchedule.WakeAt)
			require.Equal(t, wakeAt.Format(time.RFC3339Nano), *goal.ControllerSchedule.WakeAt)
		})
	}
}

func scheduleStringPtr(value string) *string {
	return &value
}
