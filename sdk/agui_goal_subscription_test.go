package sdk

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	aguistore "github.com/viant/agently-core/app/store/agui"
	"github.com/viant/agently-core/runtime/streaming"
)

func TestAGUIGoalSubscriptionKeepsChatProjectionSeparateAndRefreshesAuthoritativeState(t *testing.T) {
	enableAtomicGoals(t)
	server, repository, _ := atomicGoalFixture(t)
	bus := streaming.NewMemoryBus(64)
	backend := &backendClient{goalRepo: repository, goalInvoker: server, streaming: bus}
	store := aguistore.New(server)
	observed := observeSubscriptionCommits(store)
	ctx := context.Background()
	_, err := backend.CreateGoal(ctx, &CreateGoalInput{ConversationID: "goal-thread", Objective: "background work"})
	require.NoError(t, err)
	record := atomicGoalRecord(t, store, "goal.subscribe", "subscription", json.RawMessage(`{"durationSeconds":1}`))
	original, err := store.GetThread(ctx, "owner", "goal-thread")
	require.NoError(t, err)
	observerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := startSubscriptionObserver(t, cancel, func() error { return observeAGUIGoal(observerCtx, backend, observed, record, 3*time.Second) })
	require.Eventually(t, func() bool {
		return observed.afterCommit(func() bool {
			events, err := store.Replay(ctx, "owner", "goal-thread", "subscription", 0, 1000)
			return err == nil && len(events) >= 2
		})
	}, time.Second, 10*time.Millisecond)
	paused := atomicGoalRecord(t, store, "goal.pause", "pause-background", nil)
	_, err = executeAGUIGoalTransaction(ctx, backend, store, paused, "goal.pause", nil)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return observed.afterCommit(func() bool {
			events, _ := store.Replay(ctx, "owner", "goal-thread", "subscription", 0, 1000)
			for _, event := range events {
				if stringContainsGoalState(event.Event, "paused") {
					return true
				}
			}
			return false
		})
	}, time.Second, 10*time.Millisecond)
	cleared := atomicGoalRecord(t, store, "goal.clear", "clear-background", nil)
	_, err = executeAGUIGoalTransaction(ctx, backend, store, cleared, "goal.clear", nil)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return observed.afterCommit(func() bool {
			events, _ := store.Replay(ctx, "owner", "goal-thread", "subscription", 0, 1000)
			for _, event := range events {
				var value struct {
					Type    string
					Content struct{ Goal json.RawMessage }
				}
				json.Unmarshal(event.Event, &value)
				if value.Type == "ACTIVITY_SNAPSHOT" && string(value.Content.Goal) == "null" {
					return true
				}
			}
			return false
		})
	}, time.Second, 10*time.Millisecond)
	cancel()
	require.NoError(t, <-done)
	final, err := store.GetRun(ctx, "owner", "goal-thread", "subscription")
	require.NoError(t, err)
	require.Equal(t, aguistore.StatusCancelled, final.Status)
	require.Empty(t, final.TurnID)
	after, err := store.GetThread(ctx, "owner", "goal-thread")
	require.NoError(t, err)
	require.Equal(t, original.Messages, after.Messages)
	require.Equal(t, original.State, after.State)
	// Thread admission revisions can advance for independent commands, but the
	// subscription itself contributes no chat state or conversation message row.
}
func stringContainsGoalState(raw []byte, status string) bool {
	var value struct {
		Type    string
		Content struct{ Goal *AGUIGoalSnapshot }
	}
	return json.Unmarshal(raw, &value) == nil && value.Type == "ACTIVITY_SNAPSHOT" && value.Content.Goal != nil && value.Content.Goal.Status == status
}

func TestAGUIGoalSubscriptionRestartOmitsDuplicateStartAndHasBoundedLifetime(t *testing.T) {
	enableAtomicGoals(t)
	server, repository, _ := atomicGoalFixture(t)
	backend := &backendClient{goalRepo: repository, goalInvoker: server, streaming: streaming.NewMemoryBus(64)}
	store := aguistore.New(server)
	ctx := context.Background()
	record := atomicGoalRecord(t, store, "goal.subscribe", "restart-subscription", nil)
	record, err := store.Append(ctx, "owner", "goal-thread", record.RunID, record.Revision, []json.RawMessage{rawAGUI(map[string]any{"type": "RUN_STARTED", "threadId": record.ThreadID, "runId": record.RunID})}, &aguistore.Change{LeaseOwner: record.LeaseOwner})
	require.NoError(t, err)
	require.NoError(t, observeAGUIGoal(ctx, backend, store, record, 50*time.Millisecond))
	events, err := store.Replay(ctx, "owner", "goal-thread", record.RunID, 0, 1000)
	require.NoError(t, err)
	starts := 0
	for _, event := range events {
		var header struct{ Type string }
		json.Unmarshal(event.Event, &header)
		if header.Type == "RUN_STARTED" {
			starts++
		}
	}
	require.Equal(t, 1, starts)
	final, err := store.GetRun(ctx, "owner", "goal-thread", record.RunID)
	require.NoError(t, err)
	require.Equal(t, aguistore.StatusFinished, final.Status)
	for _, payload := range []string{`{"durationSeconds":0}`, `{"durationSeconds":3601}`, `{"userId":"forged"}`} {
		require.Error(t, runAGUIGoalSubscription(ctx, backend, store, record, json.RawMessage(payload)))
	}
}
