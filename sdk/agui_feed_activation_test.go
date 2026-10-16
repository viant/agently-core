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

func feedActivationContent(t *testing.T, store aguistore.Store, runID string) map[string]any {
	t.Helper()
	events, err := store.Replay(context.Background(), "owner", "goal-thread", runID, 0, 1000)
	require.NoError(t, err)
	var found map[string]any
	for _, row := range events {
		var event map[string]any
		require.NoError(t, json.Unmarshal(row.Event, &event))
		if event["activityType"] == "agently.feed" {
			found = event["content"].(map[string]any)
		}
	}
	return found
}
func TestAGUIFeedActivationUnknownDataDoesNotBecomeActiveAndForeignEventsDoNotRefresh(t *testing.T) {
	server, _, _ := atomicGoalFixture(t)
	store := observeSubscriptionCommits(aguistore.New(server))
	ctx := context.Background()
	record := atomicGoalRecord(t, store, "feed.subscribe", "unknown", json.RawMessage(`{"id":"feed","durationSeconds":1}`))
	client := &liveFeedClient{bus: streaming.NewMemoryBus(32), data: json.RawMessage(`{"rows":[{"retained":true}]}`)}
	observer, cancel := context.WithCancel(ctx)
	defer cancel()
	done := startSubscriptionObserver(t, cancel, func() error { return observeAGUIFeed(observer, client, store, record, "feed", time.Minute) })
	require.Eventually(t, func() bool {
		return store.afterCommit(func() bool { return feedActivationContent(t, store, record.RunID) != nil })
	}, time.Second, 10*time.Millisecond)
	initial := feedActivationContent(t, store, record.RunID)
	require.Nil(t, initial["active"])
	require.Equal(t, false, initial["activationKnown"])
	require.NoError(t, client.bus.Publish(ctx, &streaming.Event{Type: streaming.EventTypeToolFeedInactive, ConversationID: "foreign", FeedID: "feed"}))
	require.NoError(t, client.bus.Publish(ctx, &streaming.Event{Type: streaming.EventTypeToolFeedInactive, ConversationID: "goal-thread", FeedID: "other"}))
	time.Sleep(25 * time.Millisecond)
	client.mu.Lock()
	require.Equal(t, 1, client.reads)
	client.mu.Unlock()
	require.NoError(t, client.bus.Publish(ctx, &streaming.Event{Type: streaming.EventTypeToolFeedInactive, ConversationID: "goal-thread", FeedID: "feed", CreatedAt: time.Now()}))
	require.Eventually(t, func() bool {
		return store.afterCommit(func() bool { return feedActivationContent(t, store, record.RunID)["active"] == false })
	}, time.Second, 10*time.Millisecond)
	latest := feedActivationContent(t, store, record.RunID)
	require.Equal(t, true, latest["activationKnown"])
	require.Contains(t, string(rawAGUI(latest["feed"])), "retained")
	cancel()
	require.NoError(t, <-done)
	require.Equal(t, false, feedActivationContent(t, store, record.RunID)["active"], "stream completion is not feed removal or activation")
}
func TestAGUIFeedActivationRecoversRenewedJournalAndColdThreadStatusWithoutChatMutation(t *testing.T) {
	for _, source := range []string{"journal", "thread"} {
		t.Run(source, func(t *testing.T) {
			server, _, _ := atomicGoalFixture(t)
			store := observeSubscriptionCommits(aguistore.New(server))
			ctx := context.Background()
			record := atomicGoalRecord(t, store, "feed.subscribe", "recovered", json.RawMessage(`{"id":"feed","durationSeconds":1}`))
			thread, err := store.GetThread(ctx, "owner", "goal-thread")
			require.NoError(t, err)
			content := map[string]any{"version": "1", "feedId": "feed", "active": false, "title": "Retained", "developerOnly": false, "itemCount": 0}
			activity := rawAGUI(map[string]any{"type": "ACTIVITY_SNAPSHOT", "messageId": "feed/activity", "activityType": "agently.feed", "content": content})
			change := &aguistore.Change{LeaseOwner: record.LeaseOwner}
			if source == "thread" {
				change.Messages = rawAGUI([]any{map[string]any{"id": "feed/activity", "role": "activity", "activityType": "agently.feed", "content": content}})
				change.ExpectedThreadRevision = thread.Revision
			}
			record, err = store.Append(ctx, "owner", "goal-thread", record.RunID, record.Revision, []json.RawMessage{rawAGUI(map[string]any{"type": "RUN_STARTED", "threadId": "goal-thread", "runId": record.RunID}), activity}, change)
			require.NoError(t, err)
			if source == "thread" {
				record = atomicGoalRecord(t, store, "feed.subscribe", "cold-new", json.RawMessage(`{"id":"feed","durationSeconds":1}`))
			}
			before, err := store.GetThread(ctx, "owner", "goal-thread")
			require.NoError(t, err)
			client := &liveFeedClient{bus: streaming.NewMemoryBus(32), data: json.RawMessage(`{"rows":[1]}`)}
			observer, cancel := context.WithCancel(ctx)
			defer cancel()
			done := startSubscriptionObserver(t, cancel, func() error { return observeAGUIFeed(observer, client, store, record, "feed", time.Minute) })
			require.Eventually(t, func() bool {
				return store.afterCommit(func() bool {
					value := feedActivationContent(t, store, record.RunID)
					return value != nil && value["activationKnown"] == true
				})
			}, time.Second, 10*time.Millisecond)
			require.Equal(t, false, feedActivationContent(t, store, record.RunID)["active"])
			cancel()
			require.NoError(t, <-done)
			after, err := store.GetThread(ctx, "owner", "goal-thread")
			require.NoError(t, err)
			require.Equal(t, before.Messages, after.Messages)
			require.Equal(t, before.State, after.State)
		})
	}
}
