package sdk

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	aguistore "github.com/viant/agently-core/app/store/agui"
	"github.com/viant/agently-core/runtime/streaming"
	"github.com/viant/agently-core/sdk/api"
)

type liveFeedClient struct {
	Client
	bus   *streaming.MemoryBus
	mu    sync.Mutex
	data  json.RawMessage
	reads int
}

func (c *liveFeedClient) ListFeedSpecs() []*FeedSpec {
	return []*FeedSpec{{ID: "feed", Title: "Live", Match: FeedMatch{Service: "tool", Method: "read"}, UI: map[string]any{"type": "table"}}}
}
func (c *liveFeedClient) GetTranscript(context.Context, *GetTranscriptInput, ...TranscriptOption) (*ConversationStateResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reads++
	return &ConversationStateResponse{Feeds: []*api.ActiveFeedState{{FeedID: "feed", Data: append(json.RawMessage(nil), c.data...)}}}, nil
}
func (c *liveFeedClient) StreamEvents(ctx context.Context, input *StreamEventsInput) (streaming.Subscription, error) {
	return c.bus.Subscribe(ctx, input.Filter)
}
func TestAGUIFeedSubscriptionObservesLiveUpdatesWithoutChatMutation(t *testing.T) {
	server, _, _ := atomicGoalFixture(t)
	store := aguistore.New(server)
	ctx := context.Background()
	record := atomicGoalRecord(t, store, "feed.subscribe", "feed-subscription", json.RawMessage(`{"id":"feed","durationSeconds":1}`))
	before, err := store.GetThread(ctx, "owner", "goal-thread")
	require.NoError(t, err)
	client := &liveFeedClient{bus: streaming.NewMemoryBus(32), data: json.RawMessage(`{"rows":[{"n":1}]}`)}
	observer, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- observeAGUIFeed(observer, client, store, record, "feed", 3*time.Second) }()
	require.Eventually(t, func() bool {
		events, _ := store.Replay(ctx, "owner", "goal-thread", record.RunID, 0, 1000)
		return len(events) >= 2
	}, time.Second, 10*time.Millisecond)
	client.mu.Lock()
	client.data = json.RawMessage(`{"rows":[{"n":9007199254740993}]}`)
	client.mu.Unlock()
	require.NoError(t, client.bus.Publish(ctx, &streaming.Event{Type: streaming.EventTypeToolFeedActive, ConversationID: "goal-thread", StreamID: "goal-thread", FeedID: "feed"}))
	require.Eventually(t, func() bool {
		events, _ := store.Replay(ctx, "owner", "goal-thread", record.RunID, 0, 1000)
		return len(events) >= 3
	}, time.Second, 10*time.Millisecond)
	cancel()
	require.NoError(t, <-done)
	after, err := store.GetThread(ctx, "owner", "goal-thread")
	require.NoError(t, err)
	require.Equal(t, before.State, after.State)
	require.Equal(t, before.Messages, after.Messages)
	final, err := store.GetRun(ctx, "owner", "goal-thread", record.RunID)
	require.NoError(t, err)
	require.Equal(t, aguistore.StatusCancelled, final.Status)
	require.Empty(t, final.TurnID)
	events, err := store.Replay(ctx, "owner", "goal-thread", record.RunID, 0, 1000)
	require.NoError(t, err)
	encoded, _ := json.Marshal(events)
	require.Contains(t, string(encoded), `9007199254740993`)
	client.mu.Lock()
	require.Equal(t, 2, client.reads, "refresh occurs only for snapshots/events, never heartbeat polling")
	client.mu.Unlock()
}
