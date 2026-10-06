package agui_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	store "github.com/viant/agently-core/app/store/agui"
	"github.com/viant/agently-core/app/store/native"
	"github.com/viant/datly/bootstrap/connector"
)

// The schema is provisioned in a disposable acceptance database. Every test
// read/mutation below executes the same native Datly components as production.
func TestAGUIMySQLDurableLeaseResumeAndRevisionRaces(t *testing.T) {
	dsn := os.Getenv("AGENTLY_AGUI_MYSQL_DSN")
	if dsn == "" {
		t.Skip("AGENTLY_AGUI_MYSQL_DSN is not set")
	}
	ctx := context.Background()
	options := native.Options{Connectors: []connector.Config{{Name: "agently", Driver: "mysql", DSN: dsn}}}
	first, err := native.New(ctx, options)
	require.NoError(t, err)
	defer first.Shutdown(ctx)
	second, err := native.New(ctx, options)
	require.NoError(t, err)
	defer second.Shutdown(ctx)
	repositories := []*store.ComponentStore{store.New(first), store.New(second)}
	threadID, runID := uuid.NewString(), uuid.NewString()
	input := rawAGUIMySQLInput(threadID, runID)
	run, created, err := repositories[0].Admit(ctx, store.Admission{Principal: "owner", ThreadID: threadID, RunID: runID, TurnID: uuid.NewString(), ClientMessageID: uuid.NewString(), Input: input})
	require.NoError(t, err)
	require.True(t, created)
	spaced := threadID + " "
	_, spacedCreated, spacedErr := repositories[1].Admit(ctx, store.Admission{Principal: "owner", ThreadID: spaced, RunID: runID, TurnID: uuid.NewString(), ClientMessageID: uuid.NewString(), Input: rawAGUIMySQLInput(spaced, runID)})
	require.NoError(t, spacedErr, "opaque IDs with trailing spaces must remain distinct")
	require.True(t, spacedCreated)
	var group sync.WaitGroup
	results := make(chan error, 2)
	claims := make(chan *store.Run, 2)
	for i, repository := range repositories {
		group.Add(1)
		go func(i int, repository *store.ComponentStore) {
			defer group.Done()
			claimed, err := repository.Claim(ctx, "owner", threadID, runID, run.Revision, "observer-"+string(rune('a'+i)), time.Minute)
			if err == nil {
				claims <- claimed
			}
			results <- err
		}(i, repository)
	}
	group.Wait()
	close(results)
	close(claims)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		} else {
			require.ErrorIs(t, err, store.ErrConflict)
		}
	}
	require.Equal(t, 1, winners)
	claimed := <-claims
	heartbeat, err := repositories[1].Renew(ctx, "owner", threadID, runID, claimed.LeaseRevision, claimed.LeaseOwner, time.Minute)
	require.NoError(t, err)
	require.Greater(t, heartbeat.LeaseRevision, claimed.LeaseRevision)
	run, err = repositories[0].Append(ctx, "owner", threadID, runID, claimed.Revision, []json.RawMessage{start(threadID, runID)}, &store.Change{LeaseOwner: claimed.LeaseOwner})
	require.NoError(t, err)
	thread, err := repositories[1].GetThread(ctx, "owner", threadID)
	require.NoError(t, err)
	interrupt, _ := json.Marshal(map[string]any{"type": "RUN_FINISHED", "threadId": threadID, "runId": runID, "outcome": map[string]any{"type": "interrupt", "interrupts": []any{map[string]any{"id": "answer", "reason": "elicitation"}}}})
	run, err = repositories[1].Append(ctx, "owner", threadID, runID, run.Revision, []json.RawMessage{interrupt}, &store.Change{LeaseOwner: claimed.LeaseOwner, ExpectedThreadRevision: thread.Revision, State: json.RawMessage(`{"exact":9007199254740993}`), Messages: json.RawMessage(`[]`), Pending: json.RawMessage(`{"interrupts":[{"id":"answer","reason":"elicitation"}]}`)})
	require.NoError(t, err)
	thread, err = repositories[0].GetThread(ctx, "owner", threadID)
	require.NoError(t, err)
	require.Contains(t, string(thread.State), "9007199254740993")
	_, err = repositories[0].GetRun(ctx, "foreign", threadID, runID)
	require.True(t, errors.Is(err, store.ErrNotFound), err)
	admitted := make(chan error, 2)
	for i, repository := range repositories {
		group.Add(1)
		go func(i int, repository *store.ComponentStore) {
			defer group.Done()
			nextID := uuid.NewString()
			_, _, err := repository.Admit(ctx, store.Admission{Principal: "owner", ThreadID: threadID, RunID: nextID, TurnID: run.TurnID, PriorRunID: run.RunID, ExpectedPriorRevision: run.Revision, Input: rawAGUIMySQLInput(threadID, nextID)})
			admitted <- err
		}(i, repository)
	}
	group.Wait()
	close(admitted)
	winners = 0
	for err := range admitted {
		if err == nil {
			winners++
		} else {
			require.ErrorIs(t, err, store.ErrConflict)
		}
	}
	require.Equal(t, 1, winners, "one original handoff can admit only one continuation")
	journal, err := repositories[1].Replay(ctx, "owner", threadID, runID, 0, 256)
	require.NoError(t, err)
	require.Len(t, journal, 2)
	require.JSONEq(t, string(interrupt), string(journal[1].Event))
}
func rawAGUIMySQLInput(thread, run string) json.RawMessage {
	data, _ := json.Marshal(map[string]any{"threadId": thread, "runId": run, "messages": []any{}})
	return data
}
