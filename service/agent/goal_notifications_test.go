package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/store/data"
	convmem "github.com/viant/agently-core/app/store/data/memory"
	turnmodel "github.com/viant/agently-core/model/turn"
	asynccfg "github.com/viant/agently-core/protocol/async"
	"github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/runtime/streaming"
	goalsys "github.com/viant/agently-core/service/goal"
)

type failedGoalUsage struct{ goalsys.Store }

func (f failedGoalUsage) RecordUsage(context.Context, string, int64, int64) error {
	return errors.New("goal accounting fixture failure")
}

func TestGoalPostAccountingNotificationUsesCommittedBudgetStatus(t *testing.T) {
	ctx := context.Background()
	store := newLinkedGoalStore(t, "goal-notify", "notify-thread", "work", "")
	repo := store.(goalsys.Repository)
	budget := int64(100)
	require.NoError(t, repo.Apply(ctx, goalsys.Mutation{ID: "goal-notify", TokenBudget: goalsys.Field[*int64]{Present: true, Value: &budget}}))
	require.NoError(t, store.RecordUsage(ctx, "goal-notify", 150, 0))
	dataSvc, err := data.NewThinServiceInMemory(ctx)
	require.NoError(t, err)
	pub := &captureGoalEventPublisher{}
	service := &Service{dataService: dataSvc, conversation: convmem.New(), goalRuntime: goalsys.NewRuntime(store), streamPub: pub}
	input := &QueryInput{ConversationID: "notify-thread", RequestTime: time.Now().Add(-time.Second)}
	service.maybeContinueActiveGoal(ctx, input, &QueryOutput{}, requestctx.TurnMeta{ConversationID: "notify-thread", TurnID: "turn"}, "succeeded")
	require.True(t, pub.HasEvent(streaming.EventTypeGoalUpdated))
	actual, err := store.Current(ctx, "notify-thread")
	require.NoError(t, err)
	require.Equal(t, int64(150), actual.TokensUsed)
	require.Equal(t, goalsys.StatusBudgetLimited, actual.Status)
	require.Equal(t, string(actual.Status), pub.events[0].Status)
	pub.events = nil
	service.goalRuntime = goalsys.NewRuntime(failedGoalUsage{Store: store})
	service.maybeContinueActiveGoal(ctx, input, &QueryOutput{}, requestctx.TurnMeta{ConversationID: "notify-thread", TurnID: "failed"}, "succeeded")
	require.False(t, pub.HasEvent(streaming.EventTypeGoalUpdated), "failed persistence must not publish tentative budget state")
}

type failedGoalTransition struct {
	goalsys.Store
	attempted chan struct{}
}

func (f failedGoalTransition) Transition(context.Context, string, goalsys.Status, string) error {
	close(f.attempted)
	return errors.New("goal transition fixture failure")
}
func TestAsyncGoalNotificationFollowsPersistedBudgetAndSuppressesFailedTransition(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "committed", true: "failed"}[fail], func(t *testing.T) {
			ctx := context.Background()
			// The prior subcase may have published while its conversation guard is
			// still held. Independent fixtures must not share that runtime identity.
			suffix := uuid.NewString()
			conversationID, goalID, operationID := "async-notify-"+suffix, "async-goal-"+suffix, "notify-op-"+suffix
			spec, err := (&goalsys.ControllerSpec{ContinueMode: goalsys.ContinueModeIdleOnly, OnTurnFinished: goalsys.TurnPolicyWait, OnAsyncCompleted: goalsys.AsyncPolicyEvaluate}).Encode()
			require.NoError(t, err)
			store := newLinkedGoalStore(t, goalID, conversationID, "work", spec)
			repo := store.(goalsys.Repository)
			budget := int64(100)
			require.NoError(t, repo.Apply(ctx, goalsys.Mutation{ID: goalID, TokenBudget: goalsys.Field[*int64]{Present: true, Value: &budget}}))
			require.NoError(t, store.RecordUsage(ctx, goalID, 150, 0))
			dataSvc, err := data.NewThinServiceInMemory(ctx)
			require.NoError(t, err)
			// Resolve the exact cold native observation reads before measuring
			// the asynchronous transition. This is component setup, not a longer
			// transition deadline or a reset of the process-wide conversation guard.
			current, err := store.Current(ctx, conversationID)
			require.NoError(t, err)
			require.NotNil(t, current)
			_, err = dataSvc.GetActiveTurn(ctx, &turnmodel.ActiveTurnsInput{ConversationID: conversationID, Has: &turnmodel.ActiveTurnsInputHas{ConversationID: true}})
			require.NoError(t, err)
			_, err = dataSvc.CountQueuedTurns(ctx, &turnmodel.QueuedTotalInput{ConversationID: conversationID, Has: &turnmodel.QueuedTotalInputHas{ConversationID: true}})
			require.NoError(t, err)
			counter, ok := dataSvc.(controllerSignalCounter)
			require.True(t, ok)
			_, err = counter.CountPendingElicitations(ctx, conversationID)
			require.NoError(t, err)
			_, err = counter.CountPendingApprovals(ctx, conversationID)
			require.NoError(t, err)
			_, err = counter.CountControllerTurns(ctx, conversationID)
			require.NoError(t, err)
			manager := asynccfg.NewManager()
			pub := &captureGoalEventPublisher{}
			actualStore := store
			attempted := make(chan struct{})
			if fail {
				actualStore = failedGoalTransition{Store: store, attempted: attempted}
			}
			service := &Service{dataService: dataSvc, conversation: convmem.New(), goalRuntime: goalsys.NewRuntime(actualStore), asyncManager: manager, streamPub: pub}
			t.Logf("isolated goal=%s conversation=%s operation=%s", goalID, conversationID, operationID)
			record, _ := manager.Register(ctx, asynccfg.RegisterInput{ID: operationID, ParentConvID: conversationID, ParentTurnID: "parent", ExecutionMode: string(asynccfg.ExecutionModeDetach), Status: "started"})
			service.observeDetachedAsyncGoalCompletion(ctx, record)
			_, changed := manager.Update(ctx, asynccfg.UpdateInput{ID: operationID, Status: "completed"})
			require.True(t, changed)
			if fail {
				select {
				case <-attempted:
				case <-time.After(3 * time.Second):
					t.Fatal("transition not attempted")
				}
				require.Eventually(t, func() bool { _, exists := service.asyncGoalWatches.Load(operationID); return !exists }, time.Second, 10*time.Millisecond)
				actual, err := store.Current(ctx, conversationID)
				require.NoError(t, err)
				require.Equal(t, goalsys.StatusActive, actual.Status, "failed transition cannot persist tentative budget status")
				require.Equal(t, int64(150), actual.TokensUsed)
				require.False(t, pub.HasEvent(streaming.EventTypeGoalUpdated))
				pub.mu.Lock()
				require.Empty(t, pub.events, "failed transition must publish no tentative state")
				pub.mu.Unlock()
			} else {
				require.Eventually(t, func() bool { return pub.HasEvent(streaming.EventTypeGoalUpdated) }, 3*time.Second, 10*time.Millisecond)
				require.Eventually(t, func() bool { _, exists := service.asyncGoalWatches.Load(operationID); return !exists }, time.Second, 10*time.Millisecond)
				actual, err := store.Current(ctx, conversationID)
				require.NoError(t, err)
				require.Equal(t, goalsys.StatusBudgetLimited, actual.Status)
				require.Equal(t, int64(150), actual.TokensUsed)
				pub.mu.Lock()
				require.Len(t, pub.events, 1, "committed transition produces one invalidation")
				require.Equal(t, conversationID, pub.events[0].ConversationID)
				require.Equal(t, goalID, pub.events[0].GoalID)
				require.Equal(t, string(actual.Status), pub.events[0].Status)
				pub.mu.Unlock()
			}
		})
	}
}
