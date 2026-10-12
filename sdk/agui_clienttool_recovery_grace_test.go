package sdk

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	aguistore "github.com/viant/agently-core/app/store/agui"
	"github.com/viant/agently-core/app/store/conversation"
	iauth "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/protocol/agui"
	"github.com/viant/agently-core/runtime/clienttool"
	"github.com/viant/agently-core/service/shared/toolexec"
)

func commitRecoveryGrace(ctx context.Context, store pendingRecoveryStore, prior *aguistore.Run, input *agui.RunAgentInput, pending aguiPending) (*aguistore.Run, error) {
	events := []json.RawMessage{
		rawAGUI(map[string]any{"type": "RUN_STARTED", "threadId": prior.ThreadID, "runId": input.RunID}),
		rawAGUI(map[string]any{"type": "RUN_FINISHED", "threadId": prior.ThreadID, "runId": input.RunID, "outcome": recoveryGraceOutcome(pending)}),
	}
	next, _, err := store.AdmitPendingRecovery(ctx, aguistore.Admission{Principal: prior.Principal, ThreadID: prior.ThreadID, RunID: input.RunID, ParentRunID: prior.RunID, PriorRunID: prior.RunID, ExpectedPriorRevision: prior.Revision, TurnID: prior.TurnID, Input: rawAGUI(input)}, events, rawAGUI(pending))
	return next, err
}

func TestClientToolRecoveryGraceDatlyWatchdogImmutableAndRestart(t *testing.T) {
	future := time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)
	f := newClientTimeoutFixture(t, "", true, future)
	ctx := recoveryContext()
	before, err := f.c.store.GetThread(ctx, "owner", "thread")
	require.NoError(t, err)
	observedBefore := time.Now().UTC()
	require.NoError(t, f.c.native.ReconcileAGUIApprovals(ctx))
	prior, err := f.c.store.GetRun(ctx, "owner", "thread", "original")
	require.NoError(t, err)
	require.Equal(t, f.prior.Pending, prior.Pending)
	require.NotEmpty(t, prior.ResumedByRunID)
	next, err := f.c.store.GetRun(ctx, "owner", "thread", prior.ResumedByRunID)
	require.NoError(t, err)
	require.Equal(t, aguistore.StatusInterrupted, next.Status)
	var pending aguiPending
	require.NoError(t, json.Unmarshal(next.Pending, &pending))
	observed, err := time.Parse(time.RFC3339Nano, pending.ClientTools[0].RecoveryObservedAt)
	require.NoError(t, err)
	require.False(t, observed.Before(observedBefore))
	deadline, err := time.Parse(time.RFC3339Nano, pending.ClientTools[0].DeadlineAt)
	require.NoError(t, err)
	require.Equal(t, clientToolLegacyRecoveryGrace, deadline.Sub(observed))
	require.Equal(t, future, pending.ClientTools[1].DeadlineAt)
	require.Empty(t, pending.ClientTools[1].RecoveryObservedAt)
	require.Equal(t, f.pending.Interrupts[len(f.pending.Interrupts)-1], pending.Interrupts[len(pending.Interrupts)-1])
	require.Equal(t, pending.ClientTools[0].DeadlineAt, *pending.Interrupts[0].ExpiresAt)
	after, err := f.c.store.GetThread(ctx, "owner", "thread")
	require.NoError(t, err)
	require.Equal(t, before.Messages, after.Messages)
	require.Equal(t, before.State, after.State)
	journal, err := f.c.store.Replay(ctx, "owner", "thread", next.RunID, 0, 100)
	require.NoError(t, err)
	require.Len(t, journal, 2)
	require.NotContains(t, string(rawAGUI(journal)), "TOOL_CALL_RESULT")
	// Independent runtime reads the same immutable first observation. No new call,
	// grant or timer is created by recovery or by another watchdog sweep.
	restarted, _, shutdown := openCoordinatedApprovalBackend(t, f.workspace, &approvalCountingRegistry{})
	defer shutdown()
	adopted, err := aguistore.New(restarted.goalInvoker).GetRun(ctx, "owner", "thread", next.RunID)
	require.NoError(t, err)
	require.Equal(t, next.Pending, adopted.Pending)
	_, _, eligible, err := prepareClientToolRecoveryGrace(ctx, restarted.conv, aguistore.New(restarted.goalInvoker), adopted, pending, time.Now().Add(time.Hour))
	require.NoError(t, err)
	require.False(t, eligible)
	require.Zero(t, f.c.queries.Load())
}

func TestClientToolRecoveryGraceDatlyTerminalAfterPersistedGrace(t *testing.T) {
	f := newClientTimeoutFixtureShape(t, "", false, false)
	ctx := recoveryContext()
	input, pending, eligible, err := prepareClientToolRecoveryGrace(ctx, f.c.conv, f.c.store, f.prior, f.pending, time.Now().Add(-4*time.Minute))
	require.NoError(t, err)
	require.True(t, eligible)
	next, err := commitRecoveryGrace(ctx, f.c.store.(pendingRecoveryStore), f.prior, input, pending)
	require.NoError(t, err)
	require.NotEmpty(t, next.Pending)
	require.NoError(t, f.c.native.ReconcileAGUIApprovals(ctx))
	require.Eventually(t, func() bool {
		m, e := f.c.conv.GetMessage(ctx, "tool", conversation.WithIncludeToolCall(true))
		return e == nil && m.MessageToolCall != nil && m.MessageToolCall.Status == "failed"
	}, 5*time.Second, 10*time.Millisecond)
	m, err := f.c.conv.GetMessage(ctx, "tool", conversation.WithIncludeToolCall(true))
	require.NoError(t, err)
	require.Equal(t, clientToolTimeoutMessage, *m.MessageToolCall.ErrorMessage)
	require.Contains(t, m.GetContent(), "unknown")
	require.NotContains(t, m.GetContent(), "notDispatched")
	require.Zero(t, f.c.queries.Load())
}

func TestClientToolRecoveryGraceDatlyResultRaceAndContinuation(t *testing.T) {
	f := newClientTimeoutFixture(t, "", false)
	ctx := recoveryContext()
	input, pending, eligible, err := prepareClientToolRecoveryGrace(ctx, f.c.conv, f.c.store, f.prior, f.pending, time.Now())
	require.NoError(t, err)
	require.True(t, eligible)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, e := commitRecoveryGrace(ctx, f.c.store.(pendingRecoveryStore), f.prior, input, pending)
		results <- e
	}()
	go func() {
		defer wg.Done()
		answer := *input
		answer.RunID = "normal-result"
		_, _, e := f.c.store.Admit(ctx, aguistore.Admission{Principal: "owner", ThreadID: "thread", RunID: answer.RunID, ParentRunID: "original", PriorRunID: "original", ExpectedPriorRevision: f.prior.Revision, TurnID: "turn", Input: rawAGUI(&answer)})
		results <- e
	}()
	wg.Wait()
	close(results)
	count := 0
	for e := range results {
		if e == nil {
			count++
		} else {
			require.ErrorIs(t, e, aguistore.ErrConflict)
		}
	}
	require.Equal(t, 1, count)
	original, err := f.c.store.GetRun(ctx, "owner", "thread", "original")
	require.NoError(t, err)
	if original.ResumedByRunID == input.RunID {
		answer := recoveryGraceClientAnswer("normal-result-after-grace", f.pending.ClientTools[0].ProtocolID)
		found, selected, e := findAGUIContinuation(ctx, f.c.store, "owner", answer)
		require.NoError(t, e)
		require.Equal(t, input.RunID, found.RunID)
		require.NoError(t, preflightAGUIResume(ctx, f.c, answer, selected))
	}
	require.NoError(t, toolexec.CompleteClientToolResult(ctx, f.c.conv, f.pending.ClientTools[0], rawAGUI("success"), ""))
	m, err := f.c.conv.GetMessage(ctx, "tool", conversation.WithIncludeToolCall(true))
	require.NoError(t, err)
	require.Equal(t, "completed", m.MessageToolCall.Status)
}

func TestClientToolRecoveryGraceDatlyAtomicRollback(t *testing.T) {
	f := newClientTimeoutFixture(t, "", false)
	ctx := recoveryContext()
	input, pending, eligible, err := prepareClientToolRecoveryGrace(ctx, f.c.conv, f.c.store, f.prior, f.pending, time.Now())
	require.NoError(t, err)
	require.True(t, eligible)
	// Structurally recognized terminal phase, but schema-invalid event. Admission
	// must roll back together with append, leaving the original normal result path.
	events := []json.RawMessage{rawAGUI(map[string]any{"type": "RUN_STARTED", "threadId": "thread", "runId": input.RunID}), rawAGUI(map[string]any{"type": "RUN_FINISHED", "threadId": "wrong", "runId": input.RunID, "outcome": map[string]any{"type": "interrupt", "interrupts": pending.Interrupts}})}
	_, _, err = f.c.store.(pendingRecoveryStore).AdmitPendingRecovery(ctx, aguistore.Admission{Principal: "owner", ThreadID: "thread", RunID: input.RunID, ParentRunID: "original", PriorRunID: "original", ExpectedPriorRevision: f.prior.Revision, TurnID: "turn", Input: rawAGUI(input)}, events, rawAGUI(pending))
	require.Error(t, err)
	original, err := f.c.store.GetRun(ctx, "owner", "thread", "original")
	require.NoError(t, err)
	require.Equal(t, f.prior.Revision, original.Revision)
	require.Empty(t, original.ResumedByRunID)
	_, err = f.c.store.GetRun(ctx, "owner", "thread", input.RunID)
	require.ErrorIs(t, err, aguistore.ErrNotFound)
}

func TestClientToolRecoveryGraceDatlyOwnerCompletedAndDependencyGuards(t *testing.T) {
	f := newClientTimeoutFixture(t, "", false)
	ctx := recoveryContext()
	foreign := iauth.WithUserInfo(ctx, &iauth.UserInfo{Subject: "foreign"})
	_, _, eligible, err := prepareClientToolRecoveryGrace(foreign, f.c.conv, f.c.store, f.prior, f.pending, time.Now())
	require.NoError(t, err)
	require.False(t, eligible)
	forgedPrior := *f.prior
	forgedPrior.TurnID = "foreign-turn"
	_, _, eligible, err = prepareClientToolRecoveryGrace(ctx, f.c.conv, f.c.store, &forgedPrior, f.pending, time.Now())
	require.ErrorIs(t, err, aguistore.ErrConflict)
	require.False(t, eligible)
	dependency := f.pending
	dependency.Dependencies = []clienttool.Dependency{{}}
	_, _, eligible, err = prepareClientToolRecoveryGrace(ctx, f.c.conv, f.c.store, f.prior, dependency, time.Now())
	require.NoError(t, err)
	require.False(t, eligible)
	require.NoError(t, toolexec.CompleteClientToolResult(ctx, f.c.conv, f.pending.ClientTools[0], rawAGUI("success"), ""))
	_, _, eligible, err = prepareClientToolRecoveryGrace(ctx, f.c.conv, f.c.store, f.prior, f.pending, time.Now())
	require.NoError(t, err)
	require.False(t, eligible)
	original, err := f.c.store.GetRun(ctx, "owner", "thread", "original")
	require.NoError(t, err)
	require.Empty(t, original.ResumedByRunID)
}

func TestClientToolRecoveryGraceDatlyBothCASWinnerOrders(t *testing.T) {
	for _, migrationFirst := range []bool{false, true} {
		name := "normal-result-wins"
		if migrationFirst {
			name = "recovery-grace-wins"
		}
		t.Run(name, func(t *testing.T) {
			f := newClientTimeoutFixture(t, "", false)
			ctx := recoveryContext()
			input, pending, eligible, err := prepareClientToolRecoveryGrace(ctx, f.c.conv, f.c.store, f.prior, f.pending, time.Now())
			require.NoError(t, err)
			require.True(t, eligible)
			migrate := func() error {
				_, e := commitRecoveryGrace(ctx, f.c.store.(pendingRecoveryStore), f.prior, input, pending)
				return e
			}
			normal := func() error {
				answer := *input
				answer.RunID = "original-result"
				_, _, e := f.c.store.Admit(ctx, aguistore.Admission{Principal: "owner", ThreadID: "thread", RunID: answer.RunID, ParentRunID: "original", PriorRunID: "original", ExpectedPriorRevision: f.prior.Revision, TurnID: "turn", Input: rawAGUI(&answer)})
				return e
			}
			first, second := normal, migrate
			if migrationFirst {
				first, second = migrate, normal
			}
			// Both use the same original revision; gate only the commit order so both
			// real Datly CAS outcomes are covered independently of scheduler luck.
			committed := make(chan struct{})
			results := make(chan error, 2)
			go func() { results <- first(); close(committed) }()
			go func() { <-committed; results <- second() }()
			require.NoError(t, <-results)
			require.ErrorIs(t, <-results, aguistore.ErrConflict)
			original, e := f.c.store.GetRun(ctx, "owner", "thread", "original")
			require.NoError(t, e)
			expected := "original-result"
			if migrationFirst {
				expected = input.RunID
			}
			require.Equal(t, expected, original.ResumedByRunID)
			if migrationFirst {
				answer := recoveryGraceClientAnswer("client-after-recovery", f.pending.ClientTools[0].ProtocolID)
				found, selected, e := findAGUIContinuation(ctx, f.c.store, "owner", answer)
				require.NoError(t, e)
				require.Equal(t, input.RunID, found.RunID)
				require.NoError(t, preflightAGUIResume(ctx, f.c, answer, selected))
				_, _, e = f.c.store.Admit(ctx, aguistore.Admission{Principal: "owner", ThreadID: "thread", RunID: answer.RunID, ParentRunID: answer.ParentRunID, PriorRunID: found.RunID, ExpectedPriorRevision: found.Revision, TurnID: "turn", Input: rawAGUI(answer)})
				require.NoError(t, e)
			}
		})
	}
}

type lostRecoveryCommitReply struct {
	aguistore.Store
	writer pendingRecoveryStore
}

func (s lostRecoveryCommitReply) AdmitPendingRecovery(ctx context.Context, a aguistore.Admission, e []json.RawMessage, p json.RawMessage) (*aguistore.Run, bool, error) {
	_, _, err := s.writer.AdmitPendingRecovery(ctx, a, e, p)
	if err != nil {
		return nil, false, err
	}
	return nil, false, context.DeadlineExceeded
}
func TestClientToolRecoveryGraceDatlyLostReplyAdoptsWithoutExtension(t *testing.T) {
	f := newClientTimeoutFixture(t, "", false)
	ctx := recoveryContext()
	lost := lostRecoveryCommitReply{f.c.store, f.c.store.(pendingRecoveryStore)}
	handled, err := f.c.native.aguiClientToolRecoveryGrace(ctx, lost, f.prior, f.pending)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.False(t, handled)
	old, err := f.c.store.GetRun(ctx, "owner", "thread", "original")
	require.NoError(t, err)
	committed, err := f.c.store.GetRun(ctx, "owner", "thread", old.ResumedByRunID)
	require.NoError(t, err)
	restarted, store, close := openCoordinatedApprovalBackend(t, f.workspace, &approvalCountingRegistry{})
	defer close()
	require.NoError(t, restarted.ReconcileAGUIApprovals(ctx))
	adopted, err := store.GetRun(ctx, "owner", "thread", committed.RunID)
	require.NoError(t, err)
	require.Equal(t, committed.Revision, adopted.Revision)
	require.Equal(t, committed.Input, adopted.Input)
	require.Equal(t, committed.Pending, adopted.Pending)
	journal, err := store.Replay(ctx, "owner", "thread", adopted.RunID, 0, 100)
	require.NoError(t, err)
	require.Len(t, journal, 2)
}

func TestClientToolRecoveryGraceCallerPrefixCannotOverrideExpiry(t *testing.T) {
	observed := time.Now().Add(-4 * time.Minute)
	pending := aguiPending{ClientTools: []clienttool.PendingCall{{ID: "call", Name: "browser", RecoveryObservedAt: observed.UTC().Format(time.RFC3339Nano), DeadlineAt: observed.Add(clientToolLegacyRecoveryGrace).UTC().Format(time.RFC3339Nano)}}}
	for _, prefix := range []string{clientToolRecoveryGracePrefix, clientToolTimeoutPrefix} {
		forged := &agui.RunAgentInput{ThreadID: "thread", RunID: prefix + "forged", Messages: []agui.Message{{ID: "tool", Role: "tool", ToolCallID: "call", Content: rawAGUI("success")}}, ForwardedProps: rawAGUI(map[string]any{"clientToolRecoveryGrace": map[string]any{"observedAt": time.Now().UTC().Format(time.RFC3339Nano), "deadlineAt": time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)}})}
		require.ErrorContains(t, preflightAGUIResume(recoveryContext(), nil, forged, pending), "deadline exceeded")
	}
}

func recoveryGraceClientAnswer(runID, callID string) *agui.RunAgentInput {
	payload := rawAGUI(map[string]any{"content": "success"})
	metadata := agui.WireMetadata{}
	return &agui.RunAgentInput{ThreadID: "thread", RunID: runID, Messages: []agui.Message{}, Resume: rawAGUI([]agui.WireResumeEntry{{InterruptId: callID, Status: "resolved", Payload: &payload, Metadata: &metadata}})}
}

func TestClientToolRecoveryGraceDatlyPlainToolCalls(t *testing.T) {
	f := newClientTimeoutFixtureShape(t, "", false, false)
	ctx := recoveryContext()
	require.Empty(t, f.pending.Interrupts)
	require.NoError(t, f.c.native.ReconcileAGUIApprovals(ctx))
	original, err := f.c.store.GetRun(ctx, "owner", "thread", "original")
	require.NoError(t, err)
	require.NotEmpty(t, original.ResumedByRunID)
	next, err := f.c.store.GetRun(ctx, "owner", "thread", original.ResumedByRunID)
	require.NoError(t, err)
	require.Equal(t, aguistore.StatusInterrupted, next.Status)
	var saved aguiPending
	require.NoError(t, json.Unmarshal(next.Pending, &saved))
	require.Empty(t, saved.Interrupts)
	require.NotEmpty(t, saved.ClientTools[0].DeadlineAt)
	answer := &agui.RunAgentInput{ThreadID: "thread", RunID: "plain-client-result", Messages: []agui.Message{{ID: "tool", Role: "tool", ToolCallID: f.pending.ClientTools[0].ProtocolID, Content: rawAGUI("success")}}}
	found, pending, err := findAGUIContinuation(ctx, f.c.store, "owner", answer)
	require.NoError(t, err)
	require.Equal(t, next.RunID, found.RunID)
	require.NoError(t, preflightAGUIResume(ctx, f.c, answer, pending))
	journal, err := f.c.store.Replay(ctx, "owner", "thread", next.RunID, 0, 100)
	require.NoError(t, err)
	require.Len(t, journal, 2)
	require.Contains(t, string(journal[1].Event), "pendingToolCallIds")
	require.NotContains(t, string(journal[1].Event), "interrupts")
	require.Zero(t, f.c.queries.Load())
}
