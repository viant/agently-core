package agui_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	store "github.com/viant/agently-core/app/store/agui"
	"github.com/viant/agently-core/app/store/native"
)

func admission(thread, run string) store.Admission {
	return store.Admission{Principal: "owner", ThreadID: thread, RunID: run, TurnID: "internal-turn", Input: json.RawMessage(fmt.Sprintf(`{"threadId":%q,"runId":%q,"messages":[],"tools":[{"name":"browser","description":"client tool","parameters":{"type":"object"}}],"state":{"n":0},"forwardedProps":{"opaque":null}}`, thread, run))}
}
func start(thread, run string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"type":"RUN_STARTED","threadId":%q,"runId":%q}`, thread, run))
}

func TestDurableLifecycleAndResume(t *testing.T) {
	ctx := context.Background()
	workspace := t.TempDir()
	server, err := native.New(ctx, native.Options{WorkspaceRoot: workspace})
	require.NoError(t, err)
	repository := store.New(server)
	a := admission("thread", "run")
	run, created, err := repository.Admit(ctx, a)
	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, int64(1), run.Revision)
	replay, created, err := repository.Admit(ctx, a)
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, run, replay)
	run, err = repository.Append(ctx, "owner", "thread", "run", 1, []json.RawMessage{start("thread", "run")}, nil)
	require.NoError(t, err)
	require.Equal(t, int64(1), run.LastSequence)
	require.Equal(t, store.StatusRunning, run.Status)
	thread, err := repository.GetThread(ctx, "owner", "thread")
	require.NoError(t, err)
	interrupt := json.RawMessage(`{"type":"RUN_FINISHED","threadId":"thread","runId":"run","outcome":{"type":"interrupt","interrupts":[{"id":"approval","reason":"approval"}]}}`)
	pending := json.RawMessage(`[{"id":"approval","turnId":"internal-turn","toolCallId":"call","args":{"n":9007199254740991}}]`)
	run, err = repository.Append(ctx, "owner", "thread", "run", run.Revision, []json.RawMessage{interrupt}, &store.Change{Pending: pending, State: json.RawMessage(`{"nested":{"n":9007199254740991}}`), Messages: json.RawMessage(`[{"id":"m","role":"reasoning","content":"opaque","encryptedValue":"provider-continuation"}]`), ExpectedThreadRevision: thread.Revision})
	require.NoError(t, err)
	require.Equal(t, store.StatusInterrupted, run.Status)
	journal, err := repository.Replay(ctx, "owner", "thread", "run", 0, 1000)
	require.NoError(t, err)
	require.Len(t, journal, 2)
	require.JSONEq(t, string(interrupt), string(journal[1].Event))
	_, err = repository.Append(ctx, "owner", "thread", "run", run.Revision, []json.RawMessage{start("thread", "run")}, nil)
	require.Error(t, err)
	require.NoError(t, server.Shutdown(ctx))
	// A new native runtime must read identity, pending state, projection and replay
	// from the same provisioned database without any process-local bookkeeping.
	server, err = native.New(ctx, native.Options{WorkspaceRoot: workspace})
	require.NoError(t, err)
	defer server.Shutdown(ctx)
	repository = store.New(server)
	loaded, err := repository.GetRun(ctx, "owner", "thread", "run")
	require.NoError(t, err)
	require.Equal(t, run, loaded)
	restored, err := repository.GetThread(ctx, "owner", "thread")
	require.NoError(t, err)
	require.Equal(t, thread.Revision+1, restored.Revision)
	require.JSONEq(t, `{"nested":{"n":9007199254740991}}`, string(restored.State))
	next := admission("thread", "resume")
	next.PriorRunID = "run"
	next.ExpectedPriorRevision = run.Revision
	resumed, created, err := repository.Admit(ctx, next)
	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, "internal-turn", resumed.TurnID)
	previous, err := repository.GetRun(ctx, "owner", "thread", "run")
	require.NoError(t, err)
	require.Equal(t, "resume", previous.ResumedByRunID)
	require.Equal(t, run.Revision+1, previous.Revision)
	_, created, err = repository.Admit(ctx, next)
	require.NoError(t, err)
	require.False(t, created)
	other := admission("thread", "resume-other")
	other.PriorRunID = "run"
	other.ExpectedPriorRevision = run.Revision
	_, _, err = repository.Admit(ctx, other)
	require.Error(t, err)
}

func TestDurableStoreRejectsTamperingAndRollsBack(t *testing.T) {
	ctx := context.Background()
	server, err := native.New(ctx, native.Options{WorkspaceRoot: t.TempDir()})
	require.NoError(t, err)
	defer server.Shutdown(ctx)
	repository := store.New(server)
	a := admission("thread", "run")
	run, _, err := repository.Admit(ctx, a)
	require.NoError(t, err)
	a.InputHash = run.InputHash
	a.Input = json.RawMessage(`{"threadId":"thread","runId":"run","messages":[{"id":"tampered","role":"user","content":"changed"}]}`)
	_, _, err = repository.Admit(ctx, a)
	require.Error(t, err)
	_, err = repository.GetRun(ctx, "intruder", "thread", "run")
	require.True(t, errors.Is(err, store.ErrNotFound), "%v", err)
	_, err = repository.Replay(ctx, "intruder", "thread", "run", 0, 100)
	require.True(t, errors.Is(err, store.ErrNotFound), "%v", err)
	_, err = repository.Append(ctx, "owner", "thread", "run", 99, []json.RawMessage{start("thread", "run")}, nil)
	require.Error(t, err)
	_, err = repository.Append(ctx, "owner", "thread", "run", 1, []json.RawMessage{start("thread", "run"), json.RawMessage(`{"type":"RUN_ERROR","message":"failed"}`), json.RawMessage(`{"type":"CUSTOM","name":"too-late","value":null}`)}, nil)
	require.Error(t, err)
	unchanged, err := repository.GetRun(ctx, "owner", "thread", "run")
	require.NoError(t, err)
	require.Equal(t, run, unchanged)
	journal, err := repository.Replay(ctx, "owner", "thread", "run", 0, 100)
	require.NoError(t, err)
	require.Empty(t, journal)
}

func TestDurableAdmissionConcurrentAcrossNativeRuntimes(t *testing.T) {
	ctx := context.Background()
	workspace := t.TempDir()
	first, err := native.New(ctx, native.Options{WorkspaceRoot: workspace})
	require.NoError(t, err)
	defer first.Shutdown(ctx)
	second, err := native.New(ctx, native.Options{WorkspaceRoot: workspace})
	require.NoError(t, err)
	defer second.Shutdown(ctx)
	repositories := []*store.ComponentStore{store.New(first), store.New(second)}
	type result struct {
		created bool
		err     error
	}
	results := make(chan result, 8)
	barrier := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func(i int) {
			<-barrier
			_, created, err := repositories[i%2].Admit(ctx, admission("thread", "same-run"))
			results <- result{created, err}
		}(i)
	}
	close(barrier)
	createdCount := 0
	for i := 0; i < 8; i++ {
		r := <-results
		require.NoError(t, r.err)
		if r.created {
			createdCount++
		}
	}
	require.Equal(t, 1, createdCount)
	// Distinct external run identities must not execute one internal turn twice.
	results = make(chan result, 2)
	barrier = make(chan struct{})
	for i := 0; i < 2; i++ {
		go func(i int) {
			<-barrier
			a := admission("thread", fmt.Sprintf("fresh-%d", i))
			a.TurnID = "new-internal-turn"
			_, created, err := repositories[i].Admit(ctx, a)
			results <- result{created, err}
		}(i)
	}
	close(barrier)
	createdCount = 0
	conflicts := 0
	for i := 0; i < 2; i++ {
		r := <-results
		if r.err != nil {
			require.ErrorIs(t, r.err, store.ErrConflict)
			conflicts++
		} else if r.created {
			createdCount++
		}
	}
	require.Equal(t, 1, createdCount)
	require.Equal(t, 1, conflicts)
}

func TestDurablePendingResolutionAndClientTools(t *testing.T) {
	ctx := context.Background()
	server, err := native.New(ctx, native.Options{WorkspaceRoot: t.TempDir()})
	require.NoError(t, err)
	defer server.Shutdown(ctx)
	repository := store.New(server)
	run, _, err := repository.Admit(ctx, admission("thread", "client-call"))
	require.NoError(t, err)
	run, err = repository.Append(ctx, "owner", "thread", "client-call", run.Revision, []json.RawMessage{start("thread", "client-call"), json.RawMessage(`{"type":"RUN_FINISHED","threadId":"thread","runId":"client-call","outcome":{"type":"success","pendingToolCallIds":["browser-call"]}}`)}, &store.Change{Pending: json.RawMessage(`[{"toolCallId":"browser-call","args":"{}"}]`)})
	require.NoError(t, err)
	require.Equal(t, store.StatusInterrupted, run.Status)
	pending, err := repository.ListPending(ctx, "owner", "thread")
	require.NoError(t, err)
	require.Len(t, pending, 1)
	require.Equal(t, run, pending[0])
	next := admission("thread", "client-result")
	next.PriorRunID = run.RunID
	next.ExpectedPriorRevision = run.Revision
	_, _, err = repository.Admit(ctx, next)
	require.NoError(t, err)
	pending, err = repository.ListPending(ctx, "owner", "thread")
	require.NoError(t, err)
	require.Empty(t, pending)
}

func TestDurableObserverLeaseSurvivesRestartAndGuardsJournal(t *testing.T) {
	ctx := context.Background()
	workspace := t.TempDir()
	server, err := native.New(ctx, native.Options{WorkspaceRoot: workspace})
	require.NoError(t, err)
	repository := store.New(server)
	run, _, err := repository.Admit(ctx, admission("thread", "run"))
	require.NoError(t, err)
	claimed, err := repository.Claim(ctx, "owner", "thread", "run", run.Revision, "observer-1", 300*time.Millisecond)
	require.NoError(t, err)
	require.Equal(t, run.Revision, claimed.Revision)
	require.Equal(t, int64(1), claimed.LeaseRevision)
	_, err = repository.Claim(ctx, "owner", "thread", "run", run.Revision, "observer-2", time.Second)
	require.ErrorIs(t, err, store.ErrConflict)
	_, err = repository.Append(ctx, "owner", "thread", "run", run.Revision, []json.RawMessage{start("thread", "run")}, nil)
	require.ErrorIs(t, err, store.ErrConflict)
	run, err = repository.Append(ctx, "owner", "thread", "run", run.Revision, []json.RawMessage{start("thread", "run")}, &store.Change{LeaseOwner: "observer-1"})
	require.NoError(t, err)
	renewed, err := repository.Renew(ctx, "owner", "thread", "run", claimed.LeaseRevision, "observer-1", 300*time.Millisecond)
	require.NoError(t, err)
	require.Equal(t, run.Revision, renewed.Revision)
	require.Equal(t, int64(2), renewed.LeaseRevision)
	_, err = repository.Renew(ctx, "owner", "thread", "run", claimed.LeaseRevision, "observer-1", time.Second)
	require.ErrorIs(t, err, store.ErrConflict)
	require.NoError(t, server.Shutdown(ctx))
	server, err = native.New(ctx, native.Options{WorkspaceRoot: workspace})
	require.NoError(t, err)
	defer server.Shutdown(ctx)
	repository = store.New(server)
	restored, err := repository.GetRun(ctx, "owner", "thread", "run")
	require.NoError(t, err)
	require.Equal(t, "observer-1", restored.LeaseOwner)
	require.Equal(t, renewed.LeaseRevision, restored.LeaseRevision)
	time.Sleep(time.Until(*restored.LeaseUntil) + 10*time.Millisecond)
	recovered, err := repository.Claim(ctx, "owner", "thread", "run", restored.Revision, "observer-2", time.Second)
	require.NoError(t, err)
	require.Equal(t, "observer-2", recovered.LeaseOwner)
	require.Equal(t, restored.Revision, recovered.Revision)
	_, err = repository.Append(ctx, "owner", "thread", "run", restored.Revision, []json.RawMessage{json.RawMessage(`{"type":"CUSTOM","name":"stale-worker","value":null}`)}, &store.Change{LeaseOwner: "observer-1"})
	require.ErrorIs(t, err, store.ErrConflict)
	run, err = repository.Append(ctx, "owner", "thread", "run", restored.Revision, []json.RawMessage{json.RawMessage(`{"type":"RUN_FINISHED","threadId":"thread","runId":"run"}`)}, &store.Change{LeaseOwner: "observer-2"})
	require.NoError(t, err)
	require.Equal(t, store.StatusFinished, run.Status)
	_, err = repository.Claim(ctx, "owner", "thread", "run", run.Revision, "observer-3", time.Second)
	require.ErrorIs(t, err, store.ErrInvalidTransition)
	journal, err := repository.Replay(ctx, "owner", "thread", "run", 0, 1000)
	require.NoError(t, err)
	require.Len(t, journal, 2)
}

func TestDurableJournalFailureRollsBackProjectionAndRevision(t *testing.T) {
	ctx := context.Background()
	workspace := t.TempDir()
	server, err := native.New(ctx, native.Options{WorkspaceRoot: workspace})
	require.NoError(t, err)
	defer server.Shutdown(ctx)
	repository := store.New(server)
	original, _, err := repository.Admit(ctx, admission("thread", "run"))
	require.NoError(t, err)
	thread, err := repository.GetThread(ctx, "owner", "thread")
	require.NoError(t, err)
	// Fixture DDL deliberately fails the last generated component after earlier
	// components have written. All application persistence still uses Datly.
	db, err := sql.Open("sqlite", filepath.Join(workspace, "db", "agently-core.db"))
	require.NoError(t, err)
	defer db.Close()
	_, err = db.Exec(`CREATE TRIGGER reject_journal_fixture BEFORE INSERT ON call_payload WHEN NEW.kind='agui.event' AND CAST(NEW.inline_body AS TEXT) LIKE '%"name":"reject"%' BEGIN SELECT RAISE(ABORT,'journal-fixture-reject'); END`)
	require.NoError(t, err)
	_, err = repository.Append(ctx, "owner", "thread", "run", original.Revision, []json.RawMessage{start("thread", "run"), json.RawMessage(`{"type":"CUSTOM","name":"reject","value":null}`)}, &store.Change{State: json.RawMessage(`{"shouldRollback":true}`), ExpectedThreadRevision: thread.Revision})
	require.Error(t, err)
	after, err := repository.GetRun(ctx, "owner", "thread", "run")
	require.NoError(t, err)
	require.Equal(t, original, after)
	projection, err := repository.GetThread(ctx, "owner", "thread")
	require.NoError(t, err)
	require.Equal(t, thread, projection)
	journal, err := repository.Replay(ctx, "owner", "thread", "run", 0, 1000)
	require.NoError(t, err)
	require.Empty(t, journal)
}

func TestDurableResumeFailureRollsBackConsumption(t *testing.T) {
	ctx := context.Background()
	workspace := t.TempDir()
	server, err := native.New(ctx, native.Options{WorkspaceRoot: workspace})
	require.NoError(t, err)
	defer server.Shutdown(ctx)
	repository := store.New(server)
	original, _, err := repository.Admit(ctx, admission("thread", "run"))
	require.NoError(t, err)
	original, err = repository.Append(ctx, "owner", "thread", "run", original.Revision, []json.RawMessage{start("thread", "run"), json.RawMessage(`{"type":"RUN_FINISHED","threadId":"thread","runId":"run","outcome":{"type":"interrupt","interrupts":[{"id":"i","reason":"r"}]}}`)}, &store.Change{Pending: json.RawMessage(`[{"id":"i"}]`)})
	require.NoError(t, err)
	thread, err := repository.GetThread(ctx, "owner", "thread")
	require.NoError(t, err)
	db, err := sql.Open("sqlite", filepath.Join(workspace, "db", "agently-core.db"))
	require.NoError(t, err)
	defer db.Close()
	_, err = db.Exec(`CREATE TRIGGER reject_resume_fixture BEFORE INSERT ON run WHEN NEW.run_kind='agui' AND CAST(NEW.protocol_run_id AS TEXT)='bad-resume' BEGIN SELECT RAISE(ABORT,'resume-fixture-reject'); END`)
	require.NoError(t, err)
	candidate := admission("thread", "bad-resume")
	candidate.PriorRunID = "run"
	candidate.ExpectedPriorRevision = original.Revision
	_, _, err = repository.Admit(ctx, candidate)
	require.Error(t, err)
	after, err := repository.GetRun(ctx, "owner", "thread", "run")
	require.NoError(t, err)
	require.Equal(t, original, after)
	projection, err := repository.GetThread(ctx, "owner", "thread")
	require.NoError(t, err)
	require.Equal(t, thread, projection)
	_, err = repository.GetRun(ctx, "owner", "thread", "bad-resume")
	require.ErrorIs(t, err, store.ErrNotFound)
}

func TestDurableReplayPagesBeyondDefaultReaderLimit(t *testing.T) {
	ctx := context.Background()
	server, err := native.New(ctx, native.Options{WorkspaceRoot: t.TempDir()})
	require.NoError(t, err)
	defer server.Shutdown(ctx)
	repository := store.New(server)
	run, _, err := repository.Admit(ctx, admission("thread", "run"))
	require.NoError(t, err)
	events := []json.RawMessage{start("thread", "run")}
	for i := 0; i < 130; i++ {
		events = append(events, json.RawMessage(fmt.Sprintf(`{"type":"CUSTOM","name":"progress","value":%d}`, i)))
	}
	run, err = repository.Append(ctx, "owner", "thread", "run", run.Revision, events, nil)
	require.NoError(t, err)
	require.Equal(t, int64(131), run.LastSequence)
	all, err := repository.Replay(ctx, "owner", "thread", "run", 0, 1000)
	require.NoError(t, err)
	require.Len(t, all, 131)
	page, err := repository.Replay(ctx, "owner", "thread", "run", 100, 2)
	require.NoError(t, err)
	require.Len(t, page, 2)
	require.Equal(t, int64(101), page[0].Sequence)
	require.Equal(t, int64(102), page[1].Sequence)
	end, err := repository.Replay(ctx, "owner", "thread", "run", 131, 2)
	require.NoError(t, err)
	require.Empty(t, end)
}

func TestDurableContinuationConcurrentClaims(t *testing.T) {
	ctx := context.Background()
	workspace := t.TempDir()
	first, err := native.New(ctx, native.Options{WorkspaceRoot: workspace})
	require.NoError(t, err)
	defer first.Shutdown(ctx)
	second, err := native.New(ctx, native.Options{WorkspaceRoot: workspace})
	require.NoError(t, err)
	defer second.Shutdown(ctx)
	repositories := []*store.ComponentStore{store.New(first), store.New(second)}
	run, _, err := repositories[0].Admit(ctx, admission("thread", "paused"))
	require.NoError(t, err)
	run, err = repositories[0].Append(ctx, "owner", "thread", "paused", run.Revision, []json.RawMessage{start("thread", "paused"), json.RawMessage(`{"type":"RUN_FINISHED","threadId":"thread","runId":"paused","outcome":{"type":"interrupt","interrupts":[{"id":"i","reason":"r"}]}}`)}, nil)
	require.NoError(t, err)
	barrier := make(chan struct{})
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func(i int) {
			<-barrier
			a := admission("thread", fmt.Sprintf("resume-%d", i))
			a.PriorRunID = "paused"
			a.ExpectedPriorRevision = run.Revision
			_, _, err := repositories[i].Admit(ctx, a)
			results <- err
		}(i)
	}
	close(barrier)
	success, conflicts := 0, 0
	for i := 0; i < 2; i++ {
		err := <-results
		if err == nil {
			success++
		} else {
			require.ErrorIs(t, err, store.ErrConflict)
			conflicts++
		}
	}
	require.Equal(t, 1, success)
	require.Equal(t, 1, conflicts)
	prior, err := repositories[0].GetRun(ctx, "owner", "thread", "paused")
	require.NoError(t, err)
	require.NotEmpty(t, prior.ResumedByRunID)
	require.Equal(t, run.Revision+1, prior.Revision)
}

func TestDurableClientMessageIdentityIsThreadScopedAndNativeTurnIsIndependent(t *testing.T) {
	ctx := context.Background()
	server, err := native.New(ctx, native.Options{WorkspaceRoot: t.TempDir()})
	require.NoError(t, err)
	defer server.Shutdown(ctx)
	repository := store.New(server)
	first := admission("one", "run-one")
	first.ClientMessageID = "same-external-message"
	first.TurnID = "native-uuid-one"
	second := admission("two", "run-two")
	second.ClientMessageID = first.ClientMessageID
	second.TurnID = "native-uuid-two"
	one, created, err := repository.Admit(ctx, first)
	require.NoError(t, err)
	require.True(t, created)
	two, created, err := repository.Admit(ctx, second)
	require.NoError(t, err)
	require.True(t, created)
	require.NotEqual(t, one.TurnID, two.TurnID)
	require.Equal(t, one.ClientMessageID, two.ClientMessageID)
	fresh := admission("one", "fresh-run")
	fresh.TurnID = "new-native-uuid"
	fresh.ClientMessageID = first.ClientMessageID
	_, _, err = repository.Admit(ctx, fresh)
	require.ErrorIs(t, err, store.ErrConflict)
	intruder := first
	intruder.Principal = "intruder"
	intruder.RunID = "intruder-run"
	intruder.Input = json.RawMessage(`{"threadId":"one","runId":"intruder-run","messages":[]}`)
	_, _, err = repository.Admit(ctx, intruder)
	require.Error(t, err)
	unchanged, err := repository.GetRun(ctx, "owner", "one", "run-one")
	require.NoError(t, err)
	require.Equal(t, one, unchanged)
}
