package sdk

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	aguistore "github.com/viant/agently-core/app/store/agui"
	"github.com/viant/agently-core/app/store/native"
	"github.com/viant/agently-core/protocol/agui"
	"github.com/viant/agently-core/runtime/streaming"
)

func seedCompletedFeedFacts(t *testing.T, store aguistore.Store, principal, threadID, runID, status string, content ...map[string]any) *aguistore.Run {
	t.Helper()
	ctx := context.Background()
	run, _, err := store.Admit(ctx, aguistore.Admission{Principal: principal, ThreadID: threadID, RunID: runID, Input: rawAGUI(map[string]any{"threadId": threadID, "runId": runID, "messages": []any{}})})
	require.NoError(t, err)
	events := []json.RawMessage{rawAGUI(map[string]any{"type": "RUN_STARTED", "threadId": threadID, "runId": runID})}
	for i, fact := range content {
		events = append(events, rawAGUI(map[string]any{"type": "ACTIVITY_SNAPSHOT", "messageId": fmt.Sprintf("feed-%s-%d", runID, i), "activityType": "agently.feed", "content": fact}))
	}
	switch status {
	case aguistore.StatusFinished, aguistore.StatusCancelled:
		outcome := "success"
		if status == aguistore.StatusCancelled {
			outcome = "cancelled"
		}
		events = append(events, rawAGUI(map[string]any{"type": "RUN_FINISHED", "threadId": threadID, "runId": runID, "outcome": map[string]any{"type": outcome}}))
	case aguistore.StatusInterrupted:
		events = append(events, rawAGUI(map[string]any{"type": "RUN_FINISHED", "threadId": threadID, "runId": runID, "outcome": map[string]any{"type": "interrupt", "interrupts": []any{map[string]any{"id": "pending", "reason": "elicitation"}}}}))
	case aguistore.StatusError:
		events = append(events, rawAGUI(map[string]any{"type": "RUN_ERROR", "message": "fixture"}))
	}
	run, err = store.Append(ctx, principal, threadID, runID, run.Revision, events, nil)
	require.NoError(t, err)
	require.Equal(t, status, run.Status)
	return run
}
func recordedFeedFact(id string, active bool, at time.Time) map[string]any {
	content := map[string]any{"version": "1", "feedId": id, "active": active}
	if !at.IsZero() {
		content["activationAt"] = at.UTC().Format(time.RFC3339Nano)
	}
	return content
}
func requireRecoveredFeed(t *testing.T, store aguistore.Store, run *aguistore.Run, id string, active *bool, at time.Time) aguiFeedActivation {
	t.Helper()
	result, err := recoverAGUIFeedActivation(context.Background(), &liveFeedClient{}, store, run, id)
	require.NoError(t, err)
	if active == nil {
		require.Nil(t, result.Active)
	} else {
		require.NotNil(t, result.Active)
		require.Equal(t, *active, *result.Active)
	}
	require.True(t, result.At.Equal(at), "got original activationAt %v; want %v", result.At, at)
	return result
}
func feedBool(value bool) *bool { return &value }

func TestCompletedFeedObserverRecoveryAcrossAllStatuses(t *testing.T) {
	for _, status := range []string{aguistore.StatusFinished, aguistore.StatusError, aguistore.StatusCancelled, aguistore.StatusInterrupted, aguistore.StatusRunning} {
		t.Run(status, func(t *testing.T) {
			server, _, _ := atomicGoalFixture(t)
			store := aguistore.New(server)
			ctx := context.Background()
			at := time.Date(2026, 10, 4, 1, 2, 3, 987654321, time.UTC)
			old := seedCompletedFeedFacts(t, store, "owner", "goal-thread", "old-observer", status, recordedFeedFact("feed", false, at))
			before, err := store.GetThread(ctx, "owner", "goal-thread")
			require.NoError(t, err)
			fresh := atomicGoalRecord(t, store, "feed.subscribe", "new-observer", json.RawMessage(`{"id":"feed","durationSeconds":1}`))
			activation := requireRecoveredFeed(t, store, fresh, "feed", feedBool(false), at)
			require.Equal(t, &agui.FeedLifecycleOrigin{RunID: old.RunID, Sequence: 2}, activation.Origin)
			after, err := store.GetThread(ctx, "owner", "goal-thread")
			require.NoError(t, err)
			require.Equal(t, before.State, after.State)
			require.Equal(t, before.Messages, after.Messages)
		})
	}
}

func TestCompletedFeedRecoveryIsScopedAndUsesOriginalTimeNotRunRecency(t *testing.T) {
	server, _, _ := atomicGoalFixture(t)
	store := aguistore.New(server)
	ctx := context.Background()
	base := time.Date(2026, 10, 4, 1, 0, 0, 0, time.UTC)
	seedCompletedFeedFacts(t, store, "owner", "goal-thread", "newer-actual-transition", aguistore.StatusFinished, recordedFeedFact("feed", true, base.Add(time.Hour)))
	seedCompletedFeedFacts(t, store, "owner", "goal-thread", "more-recent-run-older-transition", aguistore.StatusFinished, recordedFeedFact("feed", false, base))
	seedCompletedFeedFacts(t, store, "owner", "goal-thread", "other-feed", aguistore.StatusFinished, recordedFeedFact("other", false, base.Add(2*time.Hour)))
	seedCompletedFeedFacts(t, store, "owner", "other-thread", "other-thread", aguistore.StatusFinished, recordedFeedFact("feed", false, base.Add(2*time.Hour)))
	seedCompletedFeedFacts(t, store, "foreign", "foreign-thread", "foreign-owner", aguistore.StatusFinished, recordedFeedFact("feed", false, base.Add(2*time.Hour)))
	// A refresh carries the original time; its new publication and run cannot revive it.
	seedCompletedFeedFacts(t, store, "owner", "goal-thread", "inherited-old-refresh", aguistore.StatusFinished, map[string]any{"version": "1", "feed": map[string]any{"feedId": "feed"}, "active": false, "activationKnown": true, "activationAt": base.Format(time.RFC3339Nano)})
	fresh := atomicGoalRecord(t, store, "feed.subscribe", "new-scoped-observer", json.RawMessage(`{"id":"feed"}`))
	requireRecoveredFeed(t, store, fresh, "feed", feedBool(true), base.Add(time.Hour))
	_, err := store.ReadFeedActivationFacts(ctx, "foreign", "goal-thread", "feed")
	require.ErrorIs(t, err, aguistore.ErrNotFound)
	requireRecoveredFeed(t, store, fresh, "missing-feed", nil, time.Time{})
}

func TestCompletedFeedRecoveryScansBeyondFirstPageAndRestarts(t *testing.T) {
	ctx := context.Background()
	workspace := t.TempDir()
	runtime, err := native.New(ctx, native.Options{WorkspaceRoot: workspace})
	require.NoError(t, err)
	store := aguistore.New(runtime)
	base := time.Date(2026, 10, 4, 1, 0, 0, 0, time.UTC)
	run := seedCompletedFeedFacts(t, store, "owner", "page-thread", "many-events", aguistore.StatusRunning, recordedFeedFact("feed", false, base))
	events := make([]json.RawMessage, 600)
	for i := range events {
		events[i] = rawAGUI(map[string]any{"type": "CUSTOM", "name": "padding", "value": i})
	}
	events = append(events, rawAGUI(map[string]any{"type": "ACTIVITY_SNAPSHOT", "messageId": "latest-feed", "activityType": "agently.feed", "content": recordedFeedFact("feed", true, base.Add(time.Hour))}), rawAGUI(map[string]any{"type": "RUN_FINISHED", "threadId": run.ThreadID, "runId": run.RunID, "outcome": map[string]any{"type": "success"}}))
	thread, err := store.GetThread(ctx, "owner", run.ThreadID)
	require.NoError(t, err)
	_, err = store.Append(ctx, "owner", run.ThreadID, run.RunID, run.Revision, events, &aguistore.Change{Messages: json.RawMessage(`[{"id":"user","role":"user","content":"unchanged"}]`), State: json.RawMessage(`{"exact":9007199254740993}`), ExpectedThreadRevision: thread.Revision})
	require.NoError(t, err)
	before, err := store.GetThread(ctx, "owner", run.ThreadID)
	require.NoError(t, err)
	require.NoError(t, runtime.Shutdown(ctx))
	runtime, err = native.New(ctx, native.Options{WorkspaceRoot: workspace})
	require.NoError(t, err)
	defer runtime.Shutdown(ctx)
	store = aguistore.New(runtime)
	fresh, _, err := store.Admit(ctx, aguistore.Admission{Principal: "owner", ThreadID: run.ThreadID, RunID: "after-restart", Input: rawAGUI(map[string]any{"threadId": run.ThreadID, "runId": "after-restart", "messages": []any{}})})
	require.NoError(t, err)
	requireRecoveredFeed(t, store, fresh, "feed", feedBool(true), base.Add(time.Hour))
	after, err := store.GetThread(ctx, "owner", run.ThreadID)
	require.NoError(t, err)
	require.Equal(t, before.Messages, after.Messages)
	require.Equal(t, before.State, after.State)
	// A genuine later fact remains observable after the previous run is terminal.
	seedCompletedFeedFacts(t, store, "owner", run.ThreadID, "post-run-transition", aguistore.StatusFinished, recordedFeedFact("feed", false, base.Add(2*time.Hour)))
	requireRecoveredFeed(t, store, fresh, "feed", feedBool(false), base.Add(2*time.Hour))
}

func TestFeedFactReducerKeepsEqualAndUndatedOppositesUnknownRegardlessScanOrder(t *testing.T) {
	at := time.Date(2026, 10, 4, 1, 0, 0, 0, time.UTC)
	for _, undated := range []bool{false, true} {
		stamp := at
		if undated {
			stamp = time.Time{}
		}
		facts := []agui.FeedLifecycleFact{{Active: false, At: stamp}, {Active: true, At: stamp}, {Active: true, At: stamp}, {Active: false, At: at.Add(-time.Hour)}}
		var permute func(int)
		permute = func(i int) {
			if i == len(facts) {
				state := aguiFeedActivation{}
				for _, fact := range facts {
					state.observe(fact)
				}
				require.Nil(t, state.Active)
				state.observe(agui.FeedLifecycleFact{Active: true, At: stamp})
				require.Nil(t, state.Active, "a subsequent equal candidate cannot clear ambiguity")
				return
			}
			for j := i; j < len(facts); j++ {
				facts[i], facts[j] = facts[j], facts[i]
				permute(i + 1)
				facts[i], facts[j] = facts[j], facts[i]
			}
		}
		permute(0)
	}
	// Equal original times can be ordered only by validated same-original-run sequence.
	state := aguiFeedActivation{}
	state.observe(agui.FeedLifecycleFact{Active: true, At: at, Origin: &agui.FeedLifecycleOrigin{RunID: "run", Sequence: 2}})
	state.observe(agui.FeedLifecycleFact{Active: false, At: at, Origin: &agui.FeedLifecycleOrigin{RunID: "run", Sequence: 3}})
	require.NotNil(t, state.Active)
	require.False(t, *state.Active)
}

type bufferedRecoveryFeedStore struct {
	aguistore.Store
	reader       aguistore.FeedActivationJournalReader
	beforeReturn func()
}

func (s bufferedRecoveryFeedStore) ReadFeedActivationFacts(ctx context.Context, principal, threadID, feedID string) ([]agui.FeedLifecycleFact, error) {
	facts, err := s.reader.ReadFeedActivationFacts(ctx, principal, threadID, feedID)
	s.beforeReturn()
	return facts, err
}

func TestFeedRecoveryMergesOlderBufferedLiveAndPersistsUnknownOriginalFact(t *testing.T) {
	for _, ambiguous := range []bool{false, true} {
		t.Run(fmt.Sprintf("ambiguous=%v", ambiguous), func(t *testing.T) {
			server, _, _ := atomicGoalFixture(t)
			baseStore := aguistore.New(server)
			ctx := context.Background()
			at := time.Date(2026, 10, 4, 1, 0, 0, 0, time.UTC)
			seedCompletedFeedFacts(t, baseStore, "owner", "goal-thread", "completed-recovery", aguistore.StatusFinished, recordedFeedFact("feed", false, at))
			record := atomicGoalRecord(t, baseStore, "feed.subscribe", "buffered-new", json.RawMessage(`{"id":"feed"}`))
			client := &liveFeedClient{bus: streaming.NewMemoryBus(32), data: json.RawMessage(`{"rows":[1]}`)}
			older := at.Add(-time.Hour)
			if ambiguous {
				older = time.Time{}
			}
			store := observeSubscriptionCommits(bufferedRecoveryFeedStore{Store: baseStore, reader: baseStore, beforeReturn: func() {
				require.NoError(t, client.bus.Publish(ctx, &streaming.Event{Type: streaming.EventTypeToolFeedActive, ConversationID: "goal-thread", FeedID: "feed", CreatedAt: older}))
			}})
			observer, cancel := context.WithCancel(ctx)
			defer cancel()
			done := startSubscriptionObserver(t, cancel, func() error { return observeAGUIFeed(observer, client, store, record, "feed", time.Minute) })
			require.Eventually(t, func() bool {
				return store.afterCommit(func() bool {
					content := feedActivationContent(t, baseStore, record.RunID)
					return content != nil && content["activationFact"] != nil
				})
			}, time.Second, 10*time.Millisecond)
			latest := feedActivationContent(t, baseStore, record.RunID)
			if ambiguous {
				require.Equal(t, false, latest["activationKnown"])
				require.Nil(t, latest["active"])
			} else {
				require.Equal(t, true, latest["activationKnown"])
				require.Equal(t, false, latest["active"])
				require.Equal(t, at.Format(time.RFC3339Nano), latest["activationAt"])
			}
			cancel()
			require.NoError(t, <-done)
			fresh := atomicGoalRecord(t, baseStore, "feed.subscribe", "after-buffered", json.RawMessage(`{"id":"feed"}`))
			if ambiguous {
				requireRecoveredFeed(t, baseStore, fresh, "feed", nil, time.Time{})
			} else {
				requireRecoveredFeed(t, baseStore, fresh, "feed", feedBool(false), at)
			}
		})
	}
}

type incompleteFeedStore struct{ aguistore.Store }

func (s incompleteFeedStore) ReadFeedActivationFacts(context.Context, string, string, string) ([]agui.FeedLifecycleFact, error) {
	return nil, fmt.Errorf("incomplete lifecycle scan")
}
func TestFeedIncompleteRecoveryNeverPublishesKnownActivation(t *testing.T) {
	server, _, _ := atomicGoalFixture(t)
	store := aguistore.New(server)
	ctx := context.Background()
	record := atomicGoalRecord(t, store, "feed.subscribe", "incomplete-observer", json.RawMessage(`{"id":"feed"}`))
	client := &liveFeedClient{bus: streaming.NewMemoryBus(8), data: json.RawMessage(`{"rows":[1]}`)}
	err := observeAGUIFeed(ctx, client, incompleteFeedStore{store}, record, "feed", time.Second)
	require.ErrorContains(t, err, "incomplete lifecycle scan")
	require.Nil(t, feedActivationContent(t, store, record.RunID))
	client.mu.Lock()
	defer client.mu.Unlock()
	require.Zero(t, client.reads)
}

type boolOnlyActivationClient struct {
	Client
	invoked bool
}

func (c *boolOnlyActivationClient) AGUIFeedActivation(context.Context, string, string) (*bool, error) {
	c.invoked = true
	return feedBool(true), nil
}
func TestFeedRecoveryDoesNotReplaceOriginalTimeWithBoolOnlyProviderNow(t *testing.T) {
	server, _, _ := atomicGoalFixture(t)
	store := aguistore.New(server)
	at := time.Date(2026, 10, 4, 1, 0, 0, 0, time.UTC)
	run := seedCompletedFeedFacts(t, store, "owner", "goal-thread", "dated-false", aguistore.StatusFinished, recordedFeedFact("feed", false, at))
	client := &boolOnlyActivationClient{}
	state, err := recoverAGUIFeedActivation(context.Background(), client, store, run, "feed")
	require.NoError(t, err)
	require.NotNil(t, state.Active)
	require.False(t, *state.Active)
	require.True(t, state.At.Equal(at))
	require.False(t, client.invoked)
}

func TestCompletedFeedRecoveryOpposingFactsRemainUnknownAcrossRunScanOrders(t *testing.T) {
	for _, undated := range []bool{false, true} {
		for _, first := range []bool{false, true} {
			t.Run(fmt.Sprintf("undated=%v/first=%v", undated, first), func(t *testing.T) {
				server, _, _ := atomicGoalFixture(t)
				store := aguistore.New(server)
				at := time.Date(2026, 10, 4, 1, 0, 0, 0, time.UTC)
				if undated {
					at = time.Time{}
				}
				seedCompletedFeedFacts(t, store, "owner", "goal-thread", "first-key", aguistore.StatusFinished, recordedFeedFact("feed", first, at))
				seedCompletedFeedFacts(t, store, "owner", "goal-thread", "second-key", aguistore.StatusFinished, recordedFeedFact("feed", !first, at))
				seedCompletedFeedFacts(t, store, "owner", "goal-thread", "copy-first-key", aguistore.StatusFinished, recordedFeedFact("feed", first, at))
				fresh := atomicGoalRecord(t, store, "feed.subscribe", "unknown-observer", json.RawMessage(`{"id":"feed"}`))
				requireRecoveredFeed(t, store, fresh, "feed", nil, time.Time{})
			})
		}
	}
}
