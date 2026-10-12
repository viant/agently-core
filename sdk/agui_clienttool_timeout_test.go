package sdk

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	aguistore "github.com/viant/agently-core/app/store/agui"
	"github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/app/store/data"
	"github.com/viant/agently-core/app/store/native"
	convservice "github.com/viant/agently-core/internal/service/conversation"
	"github.com/viant/agently-core/protocol/agui"
	"github.com/viant/agently-core/runtime/clienttool"
	"github.com/viant/agently-core/runtime/streaming"
	"github.com/viant/agently-core/service/shared/toolexec"
)

type clientTimeoutFixture struct {
	workspace string
	c         *datlyObservedClient
	prior     *aguistore.Run
	pending   aguiPending
}

func newClientTimeoutFixture(t *testing.T, deadline string, human bool, extraDeadlines ...string) *clientTimeoutFixture {
	return newClientTimeoutFixtureShape(t, deadline, human, true, extraDeadlines...)
}
func newClientTimeoutFixtureShape(t *testing.T, deadline string, human, clientInterrupts bool, extraDeadlines ...string) *clientTimeoutFixture {
	t.Helper()
	ctx := recoveryContext()
	workspace := t.TempDir()
	runtime, err := native.New(ctx, native.Options{WorkspaceRoot: workspace})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, runtime.Shutdown(ctx)) })
	conv, err := convservice.New(ctx, runtime)
	require.NoError(t, err)
	c := &datlyObservedClient{durableAGUIClient: &durableAGUIClient{aguiTestClient: newAGUITestClient(), store: aguistore.New(runtime)}, conv: conv}
	c.bus = streaming.NewMemoryBus(64)
	c.owner = "owner"
	conv.SetStreamPublisher(c.bus)
	c.native = &backendClient{data: data.NewService(runtime), conv: conv, goalInvoker: runtime, streaming: c.bus}
	root := conversation.NewConversation()
	root.SetId("thread")
	root.SetCreatedByUserID("owner")
	root.SetStatus("active")
	require.NoError(t, conv.PatchConversations(ctx, root))
	require.NoError(t, nativeTestTurn(ctx, c, "turn", "waiting_for_user"))
	require.NoError(t, nativeTestMessage(ctx, c, "turn", "assistant", "assistant", "fixture"))
	require.NoError(t, nativeTestMessage(ctx, c, "turn", "tool", "tool", ""))
	call := conversation.NewToolCall()
	call.SetMessageID("tool")
	call.SetOpID("call")
	call.SetTurnID("turn")
	call.SetToolName("browser")
	call.SetToolKind("function")
	call.SetStatus("waiting_for_user")
	require.NoError(t, c.conv.PatchToolCall(ctx, call))
	p := clienttool.PendingCall{ID: "call", ProtocolID: agui.ProtocolToolCallID("turn", "call"), Name: "browser", ToolMessageID: "tool", AssistantMessageID: "assistant", ConversationID: "thread", TurnID: "turn", DeadlineAt: deadline}
	pending := aguiPending{ClientTools: []clienttool.PendingCall{p}, Interrupts: []agui.WireInterrupt{aguiClientToolInterrupt(p)}}
	for i, deadline := range extraDeadlines {
		suffix := fmt.Sprint(i + 2)
		require.NoError(t, nativeTestMessage(ctx, c, "turn", "tool"+suffix, "tool", ""))
		row := conversation.NewToolCall()
		row.SetMessageID("tool" + suffix)
		row.SetOpID("call" + suffix)
		row.SetTurnID("turn")
		row.SetToolName("browser")
		row.SetToolKind("function")
		row.SetStatus("waiting_for_user")
		require.NoError(t, c.conv.PatchToolCall(ctx, row))
		extra := p
		extra.ID = "call" + suffix
		extra.ProtocolID = agui.ProtocolToolCallID("turn", extra.ID)
		extra.ToolMessageID = "tool" + suffix
		extra.DeadlineAt = deadline
		pending.ClientTools = append(pending.ClientTools, extra)
		pending.Interrupts = append(pending.Interrupts, aguiClientToolInterrupt(extra))
	}
	if !clientInterrupts {
		pending.Interrupts = nil
	}
	if human {
		pending.Interrupts = append(pending.Interrupts, agui.WireInterrupt{ID: "human", Reason: "approval"})
	}
	input := rawAGUI(map[string]any{"threadId": "thread", "runId": "original", "messages": []any{map[string]any{"id": "user", "role": "user", "content": "fixture"}}, "tools": []any{map[string]any{"name": "browser", "description": "fixture", "parameters": map[string]any{"type": "object"}}}})
	prior, _, err := c.store.Admit(ctx, aguistore.Admission{Principal: "owner", ThreadID: "thread", RunID: "original", TurnID: "turn", Input: input})
	require.NoError(t, err)
	thread, err := c.store.GetThread(ctx, "owner", "thread")
	require.NoError(t, err)
	calls := []any{}
	for _, call := range pending.ClientTools {
		calls = append(calls, map[string]any{"id": call.ProtocolID, "type": "function", "function": map[string]any{"name": "browser", "arguments": "{}"}})
	}
	messages := rawAGUI([]any{map[string]any{"id": "user", "role": "user", "content": "fixture"}, map[string]any{"id": "assistant", "role": "assistant", "toolCalls": calls}})
	prior, err = c.store.Append(ctx, "owner", "thread", "original", prior.Revision, []json.RawMessage{rawAGUI(map[string]any{"type": "RUN_STARTED", "threadId": "thread", "runId": "original"}), rawAGUI(map[string]any{"type": "MESSAGES_SNAPSHOT", "messages": json.RawMessage(messages)}), rawAGUI(map[string]any{"type": "RUN_FINISHED", "threadId": "thread", "runId": "original", "outcome": recoveryGraceOutcome(pending)})}, &aguistore.Change{Pending: rawAGUI(pending), Messages: messages, ExpectedThreadRevision: thread.Revision})
	require.NoError(t, err)
	return &clientTimeoutFixture{workspace, c, prior, pending}
}
func TestClientToolTimeoutDatlyDeadlineAndHumanBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, deadline string
		human, due     bool
	}{
		{"due", time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano), false, true},
		{"future", time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano), false, false},
		{"legacy-no-deadline", "", false, false},
		{"human-approval", time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano), true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newClientTimeoutFixture(t, tc.deadline, tc.human)
			input, due, err := prepareClientToolTimeout(recoveryContext(), f.c.conv, f.c.store, f.prior, f.pending, time.Now())
			require.NoError(t, err)
			require.Equal(t, tc.due, due)
			if due {
				require.Empty(t, input.Tools)
				require.Contains(t, string(input.Resume), "clientToolDeadlineExceeded")
				require.Contains(t, string(input.Resume), "unknown")
				require.NotContains(t, string(input.Resume), "notDispatched")
			}
			original, err := f.c.store.GetRun(recoveryContext(), "owner", "thread", "original")
			require.NoError(t, err)
			require.Equal(t, f.prior.Revision, original.Revision)
			require.Empty(t, original.ResumedByRunID)
		})
	}
}
func TestClientToolTimeoutDatlyCompletionWithoutPage(t *testing.T) {
	f := newClientTimeoutFixture(t, time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano), false)
	require.NoError(t, f.c.native.ReconcileAGUIApprovals(recoveryContext()))
	require.Eventually(t, func() bool {
		m, e := f.c.conv.GetMessage(recoveryContext(), "tool", conversation.WithIncludeToolCall(true))
		return e == nil && m.MessageToolCall != nil && m.MessageToolCall.Status == "failed"
	}, 5*time.Second, 10*time.Millisecond)
	message, err := f.c.conv.GetMessage(recoveryContext(), "tool", conversation.WithIncludeToolCall(true))
	require.NoError(t, err)
	require.Equal(t, clientToolTimeoutMessage, *message.MessageToolCall.ErrorMessage)
	require.Contains(t, message.GetContent(), "unknown")
	var terminal *aguistore.Run
	require.Eventually(t, func() bool {
		prior, e := f.c.store.GetRun(recoveryContext(), "owner", "thread", "original")
		if e != nil || prior.ResumedByRunID == "" {
			return false
		}
		terminal, e = f.c.store.GetRun(recoveryContext(), "owner", "thread", prior.ResumedByRunID)
		return e == nil && aguiRunTerminal(terminal.Status)
	}, 5*time.Second, 10*time.Millisecond)
	journal, err := f.c.store.Replay(recoveryContext(), "owner", "thread", terminal.RunID, 0, 100)
	require.NoError(t, err)
	resultSequence, errorSequence := int64(0), int64(0)
	for _, event := range journal {
		var value map[string]any
		require.NoError(t, json.Unmarshal(event.Event, &value))
		if value["type"] == "TOOL_CALL_RESULT" {
			require.Zero(t, resultSequence)
			resultSequence = event.Sequence
		}
		if value["type"] == "RUN_ERROR" {
			errorSequence = event.Sequence
		}
	}
	require.Positive(t, resultSequence)
	// This fixture deliberately has no configured model. Its continuation may
	// fail, but only after the immutable original tool timeout was committed.
	require.Greater(t, errorSequence, resultSequence)
	// Repeating the internal completion is idempotent; a late contradictory client
	// success cannot overwrite the original failed result.
	content := rawAGUI(string(rawAGUI(map[string]string{"code": "clientToolDeadlineExceeded", "dispatchState": "unknown", "outcome": "unknown"})))
	require.NoError(t, toolexec.CompleteClientToolResult(recoveryContext(), f.c.conv, f.pending.ClientTools[0], content, clientToolTimeoutMessage))
	require.ErrorContains(t, toolexec.CompleteClientToolResult(recoveryContext(), f.c.conv, f.pending.ClientTools[0], rawAGUI("late success"), ""), "different result")
	require.Zero(t, f.c.queries.Load())
}
func TestClientToolTimeoutDatlyOriginalCASAllowsOnlyOneSuccessor(t *testing.T) {
	f := newClientTimeoutFixture(t, time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano), false)
	ctx := recoveryContext()
	input, due, err := prepareClientToolTimeout(ctx, f.c.conv, f.c.store, f.prior, f.pending, time.Now())
	require.NoError(t, err)
	require.True(t, due)
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, id := range []string{input.RunID, "client-result-successor"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			copy := *input
			copy.RunID = id
			_, _, e := f.c.store.Admit(ctx, aguistore.Admission{Principal: "owner", ThreadID: "thread", RunID: id, ParentRunID: "original", PriorRunID: "original", ExpectedPriorRevision: f.prior.Revision, TurnID: "turn", Input: rawAGUI(&copy)})
			results <- e
		}(id)
	}
	wg.Wait()
	close(results)
	success := 0
	for e := range results {
		if e == nil {
			success++
		}
	}
	require.Equal(t, 1, success)
	latest, err := f.c.store.GetRun(ctx, "owner", "thread", "original")
	require.NoError(t, err)
	require.NotEmpty(t, latest.ResumedByRunID)
	// A separate Datly-backed store instance reads the retained immutable winner.
	restarted := aguistore.New(f.c.native.goalInvoker)
	winner, err := restarted.GetRun(ctx, "owner", "thread", latest.ResumedByRunID)
	require.NoError(t, err)
	require.Equal(t, "original", winner.PriorRunID)
	if winner.RunID == input.RunID {
		restored, due, e := prepareClientToolTimeout(ctx, f.c.conv, restarted, latest, f.pending, time.Now())
		require.NoError(t, e)
		require.True(t, due)
		require.Equal(t, rawAGUI(input), rawAGUI(restored))
	}
}

func TestClientToolTimeoutDatlyPreservesHumanWait(t *testing.T) {
	f := newClientTimeoutFixture(t, time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano), true)
	require.NoError(t, f.c.native.ReconcileAGUIApprovals(recoveryContext()))
	var successor *aguistore.Run
	require.Eventually(t, func() bool {
		prior, e := f.c.store.GetRun(recoveryContext(), "owner", "thread", "original")
		if e != nil || prior.ResumedByRunID == "" {
			return false
		}
		successor, e = f.c.store.GetRun(recoveryContext(), "owner", "thread", prior.ResumedByRunID)
		return e == nil && successor.Status == aguistore.StatusInterrupted
	}, 5*time.Second, 10*time.Millisecond)
	var remaining aguiPending
	require.NoError(t, json.Unmarshal(successor.Pending, &remaining))
	require.Empty(t, remaining.ClientTools)
	require.Len(t, remaining.Interrupts, 1)
	require.Equal(t, "human", remaining.Interrupts[0].ID)
	require.Equal(t, "approval", remaining.Interrupts[0].Reason)
	message, err := f.c.conv.GetMessage(recoveryContext(), "tool", conversation.WithIncludeToolCall(true))
	require.NoError(t, err)
	require.Equal(t, "failed", message.MessageToolCall.Status)
	require.Zero(t, f.c.queries.Load())
}
func TestClientToolTimeoutDatlyExistingSuccessIsNeverOverwritten(t *testing.T) {
	f := newClientTimeoutFixture(t, time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano), false)
	require.NoError(t, toolexec.CompleteClientToolResult(recoveryContext(), f.c.conv, f.pending.ClientTools[0], rawAGUI("original success"), ""))
	_, due, err := prepareClientToolTimeout(recoveryContext(), f.c.conv, f.c.store, f.prior, f.pending, time.Now())
	require.NoError(t, err)
	require.False(t, due)
	message, err := f.c.conv.GetMessage(recoveryContext(), "tool", conversation.WithIncludeToolCall(true))
	require.NoError(t, err)
	require.Equal(t, "completed", message.MessageToolCall.Status)
	require.Equal(t, "original success", message.GetContent())
}

func TestClientToolDeadlineRejectsLateFrontendResultWithoutOverridingExpiry(t *testing.T) {
	pending := aguiPending{ClientTools: []clienttool.PendingCall{{ID: "call", Name: "browser", DeadlineAt: time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano)}}}
	input := &agui.RunAgentInput{ThreadID: "thread", RunID: "late", Messages: []agui.Message{{ID: "tool", Role: "tool", ToolCallID: "call", Content: rawAGUI("late")}}}
	require.ErrorContains(t, preflightAGUIResume(recoveryContext(), nil, input, pending), "deadline exceeded")
	pending.ClientTools[0].DeadlineAt = "invalid"
	require.ErrorContains(t, preflightAGUIResume(recoveryContext(), nil, input, pending), "deadline is invalid")
}

func TestClientToolTimeoutDatlyRestartAdoptsCommittedSuccessor(t *testing.T) {
	f := newClientTimeoutFixture(t, time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano), false)
	ctx := recoveryContext()
	input, due, err := prepareClientToolTimeout(ctx, f.c.conv, f.c.store, f.prior, f.pending, time.Now())
	require.NoError(t, err)
	require.True(t, due)
	admitted, fresh, err := f.c.store.Admit(ctx, aguistore.Admission{Principal: "owner", ThreadID: "thread", RunID: input.RunID, ParentRunID: "original", PriorRunID: "original", ExpectedPriorRevision: f.prior.Revision, TurnID: "turn", Input: rawAGUI(input)})
	require.NoError(t, err)
	require.True(t, fresh)
	require.Equal(t, aguistore.StatusAdmitted, admitted.Status)
	// New runtime owns an independent Datly component server over the same
	// disposable durable store. No page, timer, or retained SDK binding is used.
	restarted, store, close := openCoordinatedApprovalBackend(t, f.workspace, &approvalCountingRegistry{})
	defer close()
	require.NoError(t, restarted.ReconcileAGUIApprovals(ctx))
	require.Eventually(t, func() bool {
		m, e := restarted.conv.GetMessage(ctx, "tool", conversation.WithIncludeToolCall(true))
		return e == nil && m.MessageToolCall != nil && m.MessageToolCall.Status == "failed"
	}, 5*time.Second, 10*time.Millisecond)
	latest, err := store.GetRun(ctx, "owner", "thread", "original")
	require.NoError(t, err)
	require.Equal(t, admitted.RunID, latest.ResumedByRunID)
	require.NoError(t, restarted.ReconcileAGUIApprovals(ctx))
	after, err := store.GetRun(ctx, "owner", "thread", "original")
	require.NoError(t, err)
	require.Equal(t, latest.ResumedByRunID, after.ResumedByRunID)
}

func TestClientToolTimeoutDatlyRestartAfterStartedJournal(t *testing.T) {
	f := newClientTimeoutFixture(t, time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano), true)
	ctx := recoveryContext()
	input, due, err := prepareClientToolTimeout(ctx, f.c.conv, f.c.store, f.prior, f.pending, time.Now())
	require.NoError(t, err)
	require.True(t, due)
	admitted, _, err := f.c.store.Admit(ctx, aguistore.Admission{Principal: "owner", ThreadID: "thread", RunID: input.RunID, ParentRunID: "original", PriorRunID: "original", ExpectedPriorRevision: f.prior.Revision, TurnID: "turn", Input: rawAGUI(input)})
	require.NoError(t, err)
	_, err = f.c.store.Append(ctx, "owner", "thread", admitted.RunID, admitted.Revision, []json.RawMessage{
		rawAGUI(map[string]any{"type": "RUN_STARTED", "threadId": "thread", "runId": admitted.RunID, "metadata": map[string]any{"agently": map[string]any{"identityVersion": "1", "nativeTurnId": "turn"}}}),
		rawAGUI(map[string]any{"type": "MESSAGES_SNAPSHOT", "messages": input.Messages}),
	}, nil)
	require.NoError(t, err)
	restarted, store, close := openCoordinatedApprovalBackend(t, f.workspace, &approvalCountingRegistry{})
	defer close()
	require.NoError(t, restarted.ReconcileAGUIApprovals(ctx))
	require.Eventually(t, func() bool {
		r, e := store.GetRun(ctx, "owner", "thread", admitted.RunID)
		return e == nil && r.Status == aguistore.StatusInterrupted
	}, 5*time.Second, 10*time.Millisecond)
	r, err := store.GetRun(ctx, "owner", "thread", admitted.RunID)
	require.NoError(t, err)
	var pending aguiPending
	require.NoError(t, json.Unmarshal(r.Pending, &pending))
	require.Empty(t, pending.ClientTools)
	require.Len(t, pending.Interrupts, 1)
	require.Equal(t, "human", pending.Interrupts[0].ID)
	message, err := restarted.conv.GetMessage(ctx, "tool", conversation.WithIncludeToolCall(true))
	require.NoError(t, err)
	require.Equal(t, "failed", message.MessageToolCall.Status)
}
func TestClientToolDeadlineRetainsExplicitCancellation(t *testing.T) {
	call := clienttool.PendingCall{ID: "call", Name: "browser", DeadlineAt: time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano)}
	pending := aguiPending{ClientTools: []clienttool.PendingCall{call}, Interrupts: []agui.WireInterrupt{aguiClientToolInterrupt(call)}}
	input := &agui.RunAgentInput{ThreadID: "thread", RunID: "cancel", Messages: []agui.Message{}, Resume: rawAGUI([]agui.WireResumeEntry{{InterruptId: "call", Status: "cancelled"}})}
	require.NoError(t, preflightAGUIResume(recoveryContext(), nil, input, pending))
}

func TestClientToolTimeoutDatlyPartialDeadlineKeepsFutureCall(t *testing.T) {
	future := time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)
	f := newClientTimeoutFixture(t, time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano), false, future)
	ctx := recoveryContext()
	require.NoError(t, f.c.native.ReconcileAGUIApprovals(ctx))
	var successor *aguistore.Run
	require.Eventually(t, func() bool {
		original, e := f.c.store.GetRun(ctx, "owner", "thread", "original")
		if e != nil || original.ResumedByRunID == "" {
			return false
		}
		successor, e = f.c.store.GetRun(ctx, "owner", "thread", original.ResumedByRunID)
		return e == nil && successor.Status == aguistore.StatusInterrupted
	}, 5*time.Second, 10*time.Millisecond)
	var pending aguiPending
	require.NoError(t, json.Unmarshal(successor.Pending, &pending))
	require.Len(t, pending.ClientTools, 1)
	require.Equal(t, "call2", pending.ClientTools[0].ID)
	require.Equal(t, future, pending.ClientTools[0].DeadlineAt)
	first, err := f.c.conv.GetMessage(ctx, "tool", conversation.WithIncludeToolCall(true))
	require.NoError(t, err)
	require.Equal(t, "failed", first.MessageToolCall.Status)
	second, err := f.c.conv.GetMessage(ctx, "tool2", conversation.WithIncludeToolCall(true))
	require.NoError(t, err)
	require.Equal(t, "waiting_for_user", second.MessageToolCall.Status)
	_, due, err := prepareClientToolTimeout(ctx, f.c.conv, f.c.store, successor, pending, time.Now())
	require.NoError(t, err)
	require.False(t, due)
}
