package sdk

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	aguistore "github.com/viant/agently-core/app/store/agui"
)

func stateCommandRecord(t *testing.T, store aguistore.Store, operation, runID string, payload json.RawMessage) *aguistore.Run {
	t.Helper()
	return atomicGoalRecord(t, store, operation, runID, payload)
}
func TestAGUIStateCommandsPersistAllPatchOperationsAndOpaquePrecondition(t *testing.T) {
	server, _, _ := atomicGoalFixture(t)
	store := aguistore.New(server)
	ctx := context.Background()
	initial, err := store.GetThread(ctx, "owner", "goal-thread")
	require.Error(t, err)
	require.Nil(t, initial)
	get := stateCommandRecord(t, store, "state.get", "get", nil)
	require.NoError(t, runAGUIStateCommand(ctx, store, get, "state.get", nil))
	events, err := store.Replay(ctx, "owner", "goal-thread", "get", 0, 1000)
	require.NoError(t, err)
	require.Len(t, events, 4)
	var result struct{ Value AGUIStateResult }
	require.NoError(t, json.Unmarshal(events[2].Event, &result))
	require.Equal(t, `null`, string(result.Value.State))
	require.NotEmpty(t, result.Value.Hash)
	patch := json.RawMessage(`{"patch":[{"op":"add","path":"","value":{"a/b":{"~k":1},"arr":[1,2]}},{"op":"test","path":"/a~1b/~0k","value":1.0},{"op":"add","path":"/arr/-","value":3},{"op":"copy","from":"/a~1b","path":"/copy"},{"op":"move","from":"/copy","path":"/moved"},{"op":"replace","path":"/moved/~0k","value":9007199254740991},{"op":"remove","path":"/arr/0"}]}`)
	record := stateCommandRecord(t, store, "state.patch", "patch", patch)
	require.NoError(t, runAGUIStateCommand(ctx, store, record, "state.patch", patch))
	thread, err := store.GetThread(ctx, "owner", "goal-thread")
	require.NoError(t, err)
	require.JSONEq(t, `{"a/b":{"~k":1},"arr":[2,3],"moved":{"~k":9007199254740991}}`, string(thread.State))
	require.Equal(t, `[]`, string(thread.Messages))
	require.NoError(t, runAGUIStateCommand(ctx, store, record, "state.patch", patch), "completed command replay must not patch twice")
	stalePayload, _ := json.Marshal(map[string]any{"patch": []any{map[string]any{"op": "replace", "path": "", "value": false}}, "ifMatch": result.Value.Hash})
	stale := stateCommandRecord(t, store, "state.patch", "stale", stalePayload)
	require.ErrorIs(t, runAGUIStateCommand(ctx, store, stale, "state.patch", stalePayload), aguistore.ErrConflict)
	hash, err := aguiStateHash(thread.State)
	require.NoError(t, err)
	payload, _ := json.Marshal(map[string]any{"patch": []any{map[string]any{"op": "replace", "path": "", "value": "scalar"}}, "ifMatch": hash})
	replace := stateCommandRecord(t, store, "state.patch", "scalar", payload)
	require.NoError(t, runAGUIStateCommand(ctx, store, replace, "state.patch", payload))
	next, err := store.GetThread(ctx, "owner", "goal-thread")
	require.NoError(t, err)
	require.Equal(t, `"scalar"`, string(next.State))
	require.Equal(t, thread.Messages, next.Messages)
}

func TestAGUIStateFailedTestAndCompetingUpdatesDoNotPartiallyWrite(t *testing.T) {
	server, _, _ := atomicGoalFixture(t)
	store := aguistore.New(server)
	ctx := context.Background()
	setup := json.RawMessage(`{"patch":[{"op":"add","path":"","value":{"n":0}}]}`)
	record := stateCommandRecord(t, store, "state.patch", "setup", setup)
	require.NoError(t, runAGUIStateCommand(ctx, store, record, "state.patch", setup))
	thread, err := store.GetThread(ctx, "owner", "goal-thread")
	require.NoError(t, err)
	bad := json.RawMessage(`{"patch":[{"op":"replace","path":"/n","value":1},{"op":"test","path":"/n","value":2}]}`)
	failed := stateCommandRecord(t, store, "state.patch", "bad", bad)
	require.Error(t, runAGUIStateCommand(ctx, store, failed, "state.patch", bad))
	unchanged, err := store.GetThread(ctx, "owner", "goal-thread")
	require.NoError(t, err)
	require.Equal(t, thread.State, unchanged.State)
	journal, err := store.Replay(ctx, "owner", "goal-thread", "bad", 0, 1000)
	require.NoError(t, err)
	require.Empty(t, journal)
	hash, err := aguiStateHash(thread.State)
	require.NoError(t, err)
	payloads := []json.RawMessage{json.RawMessage(`{"patch":[{"op":"replace","path":"/n","value":1}],"ifMatch":"` + hash + `"}`), json.RawMessage(`{"patch":[{"op":"replace","path":"/n","value":2}],"ifMatch":"` + hash + `"}`)}
	records := []*aguistore.Run{stateCommandRecord(t, store, "state.patch", "one", payloads[0]), stateCommandRecord(t, store, "state.patch", "two", payloads[1])}
	barrier := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for i := range records {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-barrier
			results <- runAGUIStateCommand(ctx, store, records[i], "state.patch", payloads[i])
		}(i)
	}
	close(barrier)
	wg.Wait()
	success, conflicts := 0, 0
	for i := 0; i < 2; i++ {
		err := <-results
		if err == nil {
			success++
		} else {
			require.ErrorIs(t, err, aguistore.ErrConflict)
			conflicts++
		}
	}
	require.Equal(t, 1, success)
	require.Equal(t, 1, conflicts)
}

type stateRevisionRaceStore struct {
	aguistore.Store
	before func()
}

func (s *stateRevisionRaceStore) Append(ctx context.Context, principal, threadID, runID string, revision int64, events []json.RawMessage, change *aguistore.Change) (*aguistore.Run, error) {
	if s.before != nil {
		before := s.before
		s.before = nil
		before()
	}
	return s.Store.Append(ctx, principal, threadID, runID, revision, events, change)
}
func TestAGUIStatePatchRetriesOnlyMessageRevisionAdvance(t *testing.T) {
	server, _, _ := atomicGoalFixture(t)
	store := aguistore.New(server)
	ctx := context.Background()
	payload := json.RawMessage(`{"patch":[{"op":"add","path":"","value":{"n":1}}]}`)
	patchRun := stateCommandRecord(t, store, "state.patch", "patch-message-race", payload)
	messageRun := stateCommandRecord(t, store, "goal.get", "messages-only", nil)
	raced := &stateRevisionRaceStore{Store: store, before: func() {
		thread, err := store.GetThread(ctx, "owner", "goal-thread")
		require.NoError(t, err)
		_, err = store.Append(ctx, "owner", "goal-thread", messageRun.RunID, messageRun.Revision, []json.RawMessage{rawAGUI(map[string]any{"type": "RUN_STARTED", "threadId": "goal-thread", "runId": messageRun.RunID}), rawAGUI(map[string]any{"type": "RUN_FINISHED", "threadId": "goal-thread", "runId": messageRun.RunID})}, &aguistore.Change{LeaseOwner: messageRun.LeaseOwner, Messages: json.RawMessage(`[{"id":"user","role":"user","content":"preserve"}]`), ExpectedThreadRevision: thread.Revision})
		require.NoError(t, err)
	}}
	require.NoError(t, runAGUIStateCommand(ctx, raced, patchRun, "state.patch", payload))
	thread, err := store.GetThread(ctx, "owner", "goal-thread")
	require.NoError(t, err)
	require.JSONEq(t, `{"n":1}`, string(thread.State))
	require.JSONEq(t, `[{"id":"user","role":"user","content":"preserve"}]`, string(thread.Messages))
}
