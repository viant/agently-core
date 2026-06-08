package goal

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/store/data"
	aggoal "github.com/viant/agently-core/pkg/agently/goal"
	aggoalwrite "github.com/viant/agently-core/pkg/agently/goal/write"
)

// fakeAccess records writes and serves a configured read view.
type fakeAccess struct {
	view     *aggoal.GoalView
	getErr   error
	patchErr error
	patched  []*aggoalwrite.MutableGoalView
}

func (f *fakeAccess) GetGoal(_ context.Context, _ string, _ *aggoal.GoalInput, _ ...data.Option) (*aggoal.GoalView, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.view, nil
}

func (f *fakeAccess) PatchGoals(_ context.Context, rows []*aggoalwrite.MutableGoalView) ([]*aggoalwrite.MutableGoalView, error) {
	if f.patchErr != nil {
		return nil, f.patchErr
	}
	f.patched = append(f.patched, rows...)
	return rows, nil
}

func strPtr(v string) *string { return &v }

func TestStore_Current(t *testing.T) {
	t.Run("nil when absent", func(t *testing.T) {
		store := NewStore(&fakeAccess{view: nil})
		got, err := store.Current(context.Background(), "c1")
		require.NoError(t, err)
		require.Nil(t, got)
	})

	t.Run("maps view to domain with controller spec", func(t *testing.T) {
		spec := `{"continueMode":"idle_only","onTurnFinished":"evaluate","onAsyncCompleted":"evaluate"}`
		access := &fakeAccess{view: &aggoal.GoalView{
			Id:             "goal-c1",
			ConversationID: "c1",
			Objective:      "ship it",
			Status:         "active",
			StatusReason:   strPtr("on track"),
			ControllerSpec: &spec,
			TokenBudget:    int64Ptr(200000),
			TokensUsed:     1000,
		}}
		got, err := NewStore(access).Current(context.Background(), "c1")
		require.NoError(t, err)
		require.NotNil(t, got)
		require.Equal(t, StatusActive, got.Status)
		require.Equal(t, "on track", got.StatusReason)
		require.NotNil(t, got.Controller)
		require.Equal(t, ContinueModeIdleOnly, got.Controller.ContinueMode)
		require.True(t, got.Autonomous())
		require.Equal(t, int64(1000), got.TokensUsed)
	})

	t.Run("invalid status surfaces an error", func(t *testing.T) {
		access := &fakeAccess{view: &aggoal.GoalView{Id: "goal-c1", Status: "retired"}}
		_, err := NewStore(access).Current(context.Background(), "c1")
		require.Error(t, err)
	})

	t.Run("invalid controller spec surfaces an error", func(t *testing.T) {
		bad := `{"continueMode":"forever"}`
		access := &fakeAccess{view: &aggoal.GoalView{Id: "goal-c1", Status: "active", ControllerSpec: &bad}}
		_, err := NewStore(access).Current(context.Background(), "c1")
		require.Error(t, err)
	})

	t.Run("propagates read errors", func(t *testing.T) {
		access := &fakeAccess{getErr: errors.New("boom")}
		_, err := NewStore(access).Current(context.Background(), "c1")
		require.Error(t, err)
	})
}

func TestStore_RecordUsage(t *testing.T) {
	access := &fakeAccess{}
	require.NoError(t, NewStore(access).RecordUsage(context.Background(), "goal-c1", 4200, 90))

	require.Len(t, access.patched, 1)
	row := access.patched[0]
	require.Equal(t, "goal-c1", row.Id)
	require.True(t, row.Has.TokensUsed)
	require.True(t, row.Has.TimeUsedSeconds)
	require.False(t, row.Has.Status)
	require.NotNil(t, row.TokensUsed)
	require.Equal(t, int64(4200), *row.TokensUsed)
	require.NotNil(t, row.TimeUsedSeconds)
	require.Equal(t, int64(90), *row.TimeUsedSeconds)
}

func TestStore_Transition(t *testing.T) {
	access := &fakeAccess{}
	require.NoError(t, NewStore(access).Transition(context.Background(), "goal-c1", StatusComplete, "objective met"))

	require.Len(t, access.patched, 1)
	row := access.patched[0]
	require.Equal(t, "goal-c1", row.Id)
	require.True(t, row.Has.Status)
	require.NotNil(t, row.Status)
	require.Equal(t, string(StatusComplete), *row.Status)
	require.True(t, row.Has.StatusReason)
	require.NotNil(t, row.StatusReason)
	require.Equal(t, "objective met", *row.StatusReason)
	require.False(t, row.Has.PauseReason)
}

func TestStore_Pause(t *testing.T) {
	access := &fakeAccess{}
	require.NoError(t, NewStore(access).Pause(context.Background(), "goal-c1", PauseReasonUserRequested))

	require.Len(t, access.patched, 1)
	row := access.patched[0]
	require.True(t, row.Has.Status)
	require.NotNil(t, row.Status)
	require.Equal(t, string(StatusPaused), *row.Status)
	require.True(t, row.Has.PauseReason)
	require.NotNil(t, row.PauseReason)
	require.Equal(t, string(PauseReasonUserRequested), *row.PauseReason)
	require.False(t, row.Has.StatusReason)
}

func TestStore_PatchErrorPropagates(t *testing.T) {
	access := &fakeAccess{patchErr: errors.New("write gate closed")}
	err := NewStore(access).Transition(context.Background(), "goal-c1", StatusBlocked, "needs creds")
	require.Error(t, err)
}
