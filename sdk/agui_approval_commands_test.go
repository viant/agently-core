package sdk

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	aguistore "github.com/viant/agently-core/app/store/agui"
	"github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/app/store/data"
	"github.com/viant/agently-core/app/store/native"
	iauth "github.com/viant/agently-core/internal/auth"
	convservice "github.com/viant/agently-core/internal/service/conversation"
	runmodel "github.com/viant/agently-core/model/run"
	toolapprovalqueuemodel "github.com/viant/agently-core/model/toolapprovalqueue"
	"github.com/viant/agently-core/protocol/agui"
	"github.com/viant/agently-core/runtime/streaming"
	agentsvc "github.com/viant/agently-core/service/agent"
)

type coordinatedApprovalFixture struct {
	client    *backendClient
	store     *aguistore.ComponentStore
	original  *aguistore.Run
	pending   aguiPending
	registry  *approvalCountingRegistry
	workspace string
	close     func()
}

func openCoordinatedApprovalBackend(t *testing.T, workspace string, registry *approvalCountingRegistry) (*backendClient, *aguistore.ComponentStore, func()) {
	t.Helper()
	ctx := recoveryContext()
	runtime, err := native.New(ctx, native.Options{WorkspaceRoot: workspace})
	require.NoError(t, err)
	conv, err := convservice.New(ctx, runtime)
	require.NoError(t, err)
	ds := data.NewService(runtime)
	// This fixture has no model/provider. A resumed native engine returns a
	// genuine configured-service error; approval effects use the real registry.
	agent := agentsvc.New(nil, nil, nil, registry, nil, conv, agentsvc.WithDataService(ds))
	client := &backendClient{agent: agent, conv: conv, data: ds, goalInvoker: runtime, registry: registry, streaming: streaming.NewMemoryBus(64)}
	conv.SetStreamPublisher(client.streaming)
	return client, aguistore.New(runtime), func() { require.NoError(t, runtime.Shutdown(ctx)) }
}
func newCoordinatedApprovalFixture(t *testing.T, approvals int, mixed bool) *coordinatedApprovalFixture {
	t.Helper()
	ctx := recoveryContext()
	workspace := t.TempDir()
	registry := &approvalCountingRegistry{stubRegistry: stubRegistry{result: "real approved result"}}
	c, store, close := openCoordinatedApprovalBackend(t, workspace, registry)
	t.Cleanup(close)
	root := conversation.NewConversation()
	root.SetId("approval-thread")
	root.SetCreatedByUserID("owner")
	root.SetVisibility("private")
	root.SetStatus("active")
	require.NoError(t, c.conv.PatchConversations(ctx, root))
	turn := conversation.NewTurn()
	turn.SetId("approval-turn")
	turn.SetConversationID("approval-thread")
	turn.SetStatus("waiting_for_user")
	require.NoError(t, c.conv.PatchTurn(ctx, turn))
	nativeRun := &runmodel.MutableRunView{}
	nativeRun.SetId("approval-turn")
	nativeRun.SetConversationID("approval-thread")
	nativeRun.SetTurnID("approval-turn")
	nativeRun.SetStatus("completed")
	nativeRun.SetEffectiveUserID("owner")
	_, err := c.data.PatchRuns(ctx, []*runmodel.MutableRunView{nativeRun})
	require.NoError(t, err)
	assistant := conversation.NewMessage()
	assistant.SetId("approval-assistant")
	assistant.SetConversationID("approval-thread")
	assistant.SetTurnID("approval-turn")
	assistant.SetRole("assistant")
	assistant.SetType("text")
	assistant.SetContent("requesting tools")
	require.NoError(t, c.conv.PatchMessage(ctx, assistant))
	pending := aguiPending{}
	var tools []any
	for i := 0; i < approvals; i++ {
		messageID := fmt.Sprintf("approval-tool-%d", i)
		op := fmt.Sprintf("backend-op-%d", i)
		queueID := fmt.Sprintf("approval-%d", i)
		message := conversation.NewMessage()
		message.SetId(messageID)
		message.SetConversationID("approval-thread")
		message.SetTurnID("approval-turn")
		message.SetRole("tool")
		message.SetType("tool_call")
		message.SetParentMessageID("approval-assistant")
		require.NoError(t, c.conv.PatchMessage(ctx, message))
		payloadID := messageID + "/request"
		payload := conversation.NewPayload()
		payload.SetId(payloadID)
		payload.SetKind("tool_request")
		payload.SetMimeType("application/json")
		payload.SetStorage("inline")
		payload.SetCompression("none")
		payload.SetSizeBytes(2)
		payload.SetInlineBody([]byte(`{}`))
		require.NoError(t, c.conv.PatchPayload(ctx, payload))
		call := conversation.NewToolCall()
		call.SetMessageID(messageID)
		call.SetTurnID("approval-turn")
		call.SetOpID(op)
		call.SetToolName("backend")
		call.SetToolKind("function")
		call.SetStatus("waiting_for_user")
		call.RequestPayloadID = &payloadID
		call.Has.RequestPayloadID = true
		require.NoError(t, c.conv.PatchToolCall(ctx, call))
		expiry := time.Now().UTC().Add(time.Hour)
		queue := &toolapprovalqueuemodel.ToolApprovalQueue{Has: &toolapprovalqueuemodel.ToolApprovalQueueHas{}}
		queue.SetId(queueID)
		queue.SetUserId("owner")
		queue.SetToolName("backend")
		queue.SetArguments([]byte(`{}`))
		queue.SetStatus("pending")
		queue.SetConversationId("approval-thread")
		queue.SetTurnId("approval-turn")
		queue.SetMessageId("approval-assistant")
		queue.SetMetadata(rawAGUI(map[string]any{"opId": op}))
		queue.SetExpiresAt(expiry)
		require.NoError(t, c.conv.(toolApprovalQueuePatcher).PatchToolApprovalQueue(ctx, queue))
		interrupt, err := aguiApprovalInterrupt(&PendingToolApproval{ID: queueID, ConversationID: "approval-thread", TurnID: "approval-turn", MessageID: "approval-assistant", ToolName: "backend", Arguments: map[string]any{}, Metadata: map[string]any{"opId": op}, ExpiresAt: &expiry})
		require.NoError(t, err)
		publicID := agui.ProtocolToolCallID("approval-turn", op)
		interrupt.ToolCallID = &publicID
		pending.Interrupts = append(pending.Interrupts, interrupt)
		tools = append(tools, map[string]any{"id": publicID, "type": "function", "function": map[string]any{"name": "backend", "arguments": "{}"}})
	}
	if mixed {
		pending.Interrupts = append(pending.Interrupts, agui.WireInterrupt{ID: "genuine-question", Reason: "elicitation"})
	}
	messages := rawAGUI([]any{map[string]any{"id": "user", "role": "user", "content": "hello"}, map[string]any{"id": "assistant", "role": "assistant", "content": "requesting tools", "toolCalls": tools}})
	input := rawAGUI(map[string]any{"threadId": "approval-thread", "runId": "original", "messages": []any{map[string]any{"id": "user", "role": "user", "content": "hello"}}, "state": map[string]any{"counter": 5}})
	original, _, err := store.Admit(ctx, aguistore.Admission{Principal: "owner", ThreadID: "approval-thread", RunID: "original", TurnID: "approval-turn", Input: input})
	require.NoError(t, err)
	thread, err := store.GetThread(ctx, "owner", "approval-thread")
	require.NoError(t, err)
	original, err = store.Append(ctx, "owner", "approval-thread", "original", original.Revision, []json.RawMessage{rawAGUI(map[string]any{"type": "RUN_STARTED", "threadId": "approval-thread", "runId": "original"}), rawAGUI(map[string]any{"type": "MESSAGES_SNAPSHOT", "messages": json.RawMessage(messages)}), rawAGUI(map[string]any{"type": "RUN_FINISHED", "threadId": "approval-thread", "runId": "original", "outcome": map[string]any{"type": "interrupt", "interrupts": pending.Interrupts}})}, &aguistore.Change{Pending: rawAGUI(pending), Messages: messages, State: json.RawMessage(`{"counter":5}`), ExpectedThreadRevision: thread.Revision})
	require.NoError(t, err)
	return &coordinatedApprovalFixture{client: c, store: store, original: original, pending: pending, registry: registry, workspace: workspace, close: close}
}
func approvalAnswer(id, action string) agui.WireResumeEntry {
	payload := rawAGUI(map[string]any{"action": action})
	status := "resolved"
	if action == "cancel" {
		status = "cancelled"
	}
	return agui.WireResumeEntry{InterruptId: id, Status: status, Payload: &payload}
}
func waitApprovalSuccessor(t *testing.T, store aguistore.Store, original *aguistore.Run) *aguistore.Run {
	t.Helper()
	var successor *aguistore.Run
	require.Eventually(t, func() bool {
		latest, err := store.GetRun(recoveryContext(), original.Principal, original.ThreadID, original.RunID)
		if err != nil || latest.ResumedByRunID == "" {
			return false
		}
		successor, err = store.GetRun(recoveryContext(), original.Principal, original.ThreadID, latest.ResumedByRunID)
		return err == nil && aguiRunTerminal(successor.Status)
	}, 3*time.Second, 10*time.Millisecond)
	return successor
}

func TestAGUIApprovalInboxImmediatePerItemReceiptsPreserveMixedInterrupts(t *testing.T) {
	f := newCoordinatedApprovalFixture(t, 2, true)
	for i := 0; i < 2; i++ {
		out, err := f.client.DecideToolApproval(recoveryContext(), &DecideToolApprovalInput{ID: fmt.Sprintf("approval-%d", i), Action: "approve"})
		require.NoError(t, err)
		require.Equal(t, "real approved result", out.Outcome.Result)
		require.Equal(t, "executed", out.Outcome.Status)
		require.NotNil(t, out.Protocol)
		require.Empty(t, out.Protocol.ContinuationRunID)
		require.Contains(t, out.Protocol.RemainingInterruptIDs, "genuine-question")
		command, err := f.store.GetRun(recoveryContext(), "owner", "approval-thread", out.Protocol.CommandRunID)
		require.NoError(t, err)
		require.Equal(t, aguistore.StatusFinished, command.Status)
		require.Empty(t, command.TurnID)
	}
	require.Equal(t, int64(2), f.registry.calls.Load())
	original, err := f.store.GetRun(recoveryContext(), "owner", "approval-thread", "original")
	require.NoError(t, err)
	require.Empty(t, original.ResumedByRunID)
	replay, err := f.client.DecideToolApproval(recoveryContext(), &DecideToolApprovalInput{ID: "approval-0", Action: "approve"})
	require.NoError(t, err)
	require.Equal(t, "executed", replay.Outcome.Status)
	require.Equal(t, int64(2), f.registry.calls.Load())
	_, err = f.client.DecideToolApproval(recoveryContext(), &DecideToolApprovalInput{ID: "approval-0", Action: "reject"})
	require.ErrorContains(t, err, "different answer")
	require.Equal(t, int64(2), f.registry.calls.Load())
}

func TestAGUIApprovalCommandHTTPHasOwnJournalAndRejectsForeignOwner(t *testing.T) {
	f := newCoordinatedApprovalFixture(t, 1, true)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal := r.Header.Get("X-Test-Principal")
		if principal == "" {
			principal = "owner"
		}
		handleAGUIRun(f.client, nil)(w, r.WithContext(iauth.WithUserInfo(r.Context(), &iauth.UserInfo{Subject: principal})))
	}))
	defer server.Close()
	payload := rawAGUI(&AGUIApprovalDecideInput{OriginalRunID: "original", ApprovalID: "approval-0", Answer: approvalAnswer("approval-0", "approve")})
	body := string(rawAGUI(map[string]any{"threadId": "approval-thread", "runId": "command", "messages": []any{}, "forwardedProps": map[string]any{"agently": map[string]any{"version": "1", "operation": "approval.decide", "requestId": "command", "payload": json.RawMessage(payload)}}}))
	var before string
	for i := 0; i < 2; i++ {
		req, err := http.NewRequest(http.MethodPost, server.URL, strings.NewReader(body))
		require.NoError(t, err)
		response, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		wire, err := io.ReadAll(response.Body)
		response.Body.Close()
		require.NoError(t, err)
		require.Equal(t, 200, response.StatusCode, string(wire))
		require.Contains(t, string(wire), `"real approved result"`)
		if i == 0 {
			before = string(wire)
		} else {
			require.Equal(t, before, string(wire))
		}
	}
	require.Equal(t, int64(1), f.registry.calls.Load())
	request, _ := http.NewRequest(http.MethodPost, server.URL, strings.NewReader(strings.Replace(body, `"command"`, `"foreign-command"`, -1)))
	request.Header.Set("X-Test-Principal", "foreign")
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	response.Body.Close()
	require.Equal(t, http.StatusForbidden, response.StatusCode)
}

func TestAGUIApprovalCompletedPreExpiryReceiptReplaysAfterExpiry(t *testing.T) {
	f := newCoordinatedApprovalFixture(t, 1, true)
	ctx := recoveryContext()
	interrupt := f.pending.Interrupts[0]
	answer := approvalAnswer(interrupt.ID, "approve")
	_, err := f.client.aguiApplyApproval(ctx, f.original, interrupt, answer)
	require.NoError(t, err)
	past := time.Now().Add(-time.Hour)
	patch := &toolapprovalqueuemodel.ToolApprovalQueue{Has: &toolapprovalqueuemodel.ToolApprovalQueueHas{}}
	patch.SetId(interrupt.ID)
	patch.SetUserId("owner")
	patch.SetExpiresAt(past)
	require.NoError(t, f.client.conv.(toolApprovalQueuePatcher).PatchToolApprovalQueue(ctx, patch))
	expiry := past.Format(time.RFC3339Nano)
	interrupt.ExpiresAt = &expiry
	pending := aguiPending{Interrupts: []agui.WireInterrupt{interrupt}}
	input := &agui.RunAgentInput{ThreadID: "approval-thread", Resume: rawAGUI([]agui.WireResumeEntry{answer})}
	require.NoError(t, preflightAGUIResume(ctx, f.client, input, pending))
	_, err = f.client.aguiApplyApproval(ctx, f.original, interrupt, answer)
	require.NoError(t, err)
	require.Equal(t, int64(1), f.registry.calls.Load())
	input.Resume = rawAGUI([]agui.WireResumeEntry{approvalAnswer(interrupt.ID, "cancel")})
	require.Error(t, preflightAGUIResume(ctx, f.client, input, pending))
}

func TestAGUIApprovalWatchdogRecoversLastReceiptGapAndCompetingSweeps(t *testing.T) {
	f := newCoordinatedApprovalFixture(t, 1, false)
	ctx := recoveryContext()
	_, err := f.client.aguiApplyApproval(ctx, f.original, f.pending.Interrupts[0], approvalAnswer("approval-0", "approve"))
	require.NoError(t, err)
	second, _, closeSecond := openCoordinatedApprovalBackend(t, f.workspace, f.registry)
	defer closeSecond()
	var wg sync.WaitGroup
	failures := make(chan error, 2)
	for _, client := range []*backendClient{f.client, second} {
		wg.Add(1)
		go func(client *backendClient) { defer wg.Done(); failures <- client.ReconcileAGUIApprovals(ctx) }(client)
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		require.NoError(t, err)
	}
	successor := waitApprovalSuccessor(t, f.store, f.original)
	require.Equal(t, f.original.RunID, successor.PriorRunID)
	require.Equal(t, f.original.TurnID, successor.TurnID)
	require.Equal(t, int64(1), f.registry.calls.Load())
	entries, err := aguiRecoveryJournal(ctx, f.store, successor)
	require.NoError(t, err)
	started := 0
	for _, entry := range entries {
		var event struct {
			Type string `json:"type"`
		}
		require.NoError(t, json.Unmarshal(entry, &event))
		if event.Type == "RUN_STARTED" {
			started++
		}
	}
	require.Equal(t, 1, started)
	runs, err := f.store.ListRunsByTurn(ctx, "owner", "approval-thread", "approval-turn")
	require.NoError(t, err)
	require.Len(t, runs, 2)
}

func TestAGUIApprovalWatchdogRecoversAdmittedSuccessorAfterRuntimeRestart(t *testing.T) {
	f := newCoordinatedApprovalFixture(t, 1, false)
	ctx := recoveryContext()
	answer := approvalAnswer("approval-0", "approve")
	_, err := f.client.aguiApplyApproval(ctx, f.original, f.pending.Interrupts[0], answer)
	require.NoError(t, err)
	input := rawAGUI(map[string]any{"threadId": "approval-thread", "runId": "admitted-before-crash", "messages": []any{}, "resume": []agui.WireResumeEntry{answer}})
	_, _, err = f.store.Admit(ctx, aguistore.Admission{Principal: "owner", ThreadID: "approval-thread", RunID: "admitted-before-crash", PriorRunID: "original", ExpectedPriorRevision: f.original.Revision, TurnID: "approval-turn", Input: input})
	require.NoError(t, err)
	f.close()
	restarted, store, closeRestarted := openCoordinatedApprovalBackend(t, f.workspace, f.registry)
	defer closeRestarted()
	require.NoError(t, restarted.ReconcileAGUIApprovals(ctx))
	successor := waitApprovalSuccessor(t, store, f.original)
	require.Equal(t, "admitted-before-crash", successor.RunID)
	require.Equal(t, int64(1), f.registry.calls.Load())
	events, err := aguiRecoveryJournal(ctx, store, successor)
	require.NoError(t, err)
	results := 0
	for _, raw := range events {
		var event struct {
			Type string `json:"type"`
		}
		require.NoError(t, json.Unmarshal(raw, &event))
		if event.Type == "TOOL_CALL_RESULT" {
			results++
		}
	}
	require.Equal(t, 1, results, "resumed worker consumes the real persisted effect receipt once")
}

func TestAGUIApprovalTimeoutUsesDurableCommandWithoutNativeHandoff(t *testing.T) {
	f := newCoordinatedApprovalFixture(t, 1, true)
	ctx := recoveryContext()
	past := time.Now().UTC().Add(-time.Hour)
	patch := &toolapprovalqueuemodel.ToolApprovalQueue{Has: &toolapprovalqueuemodel.ToolApprovalQueueHas{}}
	patch.SetId("approval-0")
	patch.SetUserId("owner")
	patch.SetExpiresAt(past)
	require.NoError(t, f.client.conv.(toolApprovalQueuePatcher).PatchToolApprovalQueue(ctx, patch))
	row, err := f.client.aguiApprovalRow(ctx, "approval-thread", f.pending.Interrupts[0])
	require.NoError(t, err)
	outcome, err := timeoutToolApproval(ctx, f.client, f.client.conv.(toolApprovalQueuePatcher), f.client.conv.(toolApprovalQueueLister), row, time.Now().UTC())
	require.NoError(t, err)
	require.Equal(t, "timed_out", outcome.Status)
	require.Equal(t, "timeout", outcome.Action)
	require.Equal(t, "approval request timed out", outcome.Result)
	require.NotNil(t, outcome.Protocol)
	require.Empty(t, outcome.Protocol.ContinuationRunID)
	require.Zero(t, f.registry.calls.Load())
	original, err := f.store.GetRun(ctx, "owner", "approval-thread", "original")
	require.NoError(t, err)
	require.Empty(t, original.ResumedByRunID)
	var snapshot aguiPending
	require.NoError(t, json.Unmarshal(original.Pending, &snapshot))
	require.Len(t, snapshot.Interrupts, 2, "unanswered mixed interrupt retained")
}

func TestAGUIApprovalRecoveryOwnershipIsPhaseSpecific(t *testing.T) {
	f := newCoordinatedApprovalFixture(t, 1, false)
	ctx := recoveryContext()
	owned, err := f.client.IsAGUIApprovalRecoveryOwnedTurn(ctx, "approval-thread", "approval-turn")
	require.NoError(t, err)
	require.True(t, owned)
	answer := approvalAnswer("approval-0", "approve")
	_, err = f.client.aguiApplyApproval(ctx, f.original, f.pending.Interrupts[0], answer)
	require.NoError(t, err)
	input := rawAGUI(map[string]any{"threadId": "approval-thread", "runId": "phase-successor", "messages": []any{}, "resume": []agui.WireResumeEntry{answer}})
	successor, _, err := f.store.Admit(ctx, aguistore.Admission{Principal: "owner", ThreadID: "approval-thread", RunID: "phase-successor", PriorRunID: "original", ExpectedPriorRevision: f.original.Revision, TurnID: "approval-turn", Input: input})
	require.NoError(t, err)
	owned, err = f.client.IsAGUIApprovalRecoveryOwnedTurn(ctx, "approval-thread", "approval-turn")
	require.NoError(t, err)
	require.True(t, owned)
	_, err = f.store.Append(ctx, "owner", "approval-thread", successor.RunID, successor.Revision, []json.RawMessage{rawAGUI(map[string]any{"type": "RUN_STARTED", "threadId": "approval-thread", "runId": successor.RunID})}, nil)
	require.NoError(t, err)
	turn := conversation.NewTurn()
	turn.SetId("approval-turn")
	turn.SetConversationID("approval-thread")
	turn.SetStatus("running")
	require.NoError(t, f.client.conv.PatchTurn(ctx, turn))
	owned, err = f.client.IsAGUIApprovalRecoveryOwnedTurn(ctx, "approval-thread", "approval-turn")
	require.NoError(t, err)
	require.False(t, owned, "native-started worker keeps native lease recovery")
	_, _, err = f.store.Admit(ctx, aguistore.Admission{Principal: "owner", ThreadID: "approval-thread", RunID: "initial-other", TurnID: "initial-native-turn", Input: rawAGUI(map[string]any{"threadId": "approval-thread", "runId": "initial-other", "messages": []any{map[string]any{"id": "user-other", "role": "user", "content": "initial"}}})})
	require.NoError(t, err)
	owned, err = f.client.IsAGUIApprovalRecoveryOwnedTurn(ctx, "approval-thread", "initial-native-turn")
	require.NoError(t, err)
	require.False(t, owned, "initial AG-UI admission does not suppress existing native recovery")
}

func TestAGUIApprovalPostCommitHintContainsNoPrivateReceipts(t *testing.T) {
	f := newCoordinatedApprovalFixture(t, 1, true)
	ctx := recoveryContext()
	sub, err := f.client.streaming.Subscribe(ctx, func(event *streaming.Event) bool {
		return event.Type == streaming.EventTypeConversationMetaUpdated && event.Patch["aguiUpdated"] == true
	})
	require.NoError(t, err)
	defer sub.Close()
	_, err = f.client.DecideToolApproval(ctx, &DecideToolApprovalInput{ID: "approval-0", Action: "approve"})
	require.NoError(t, err)
	select {
	case event := <-sub.C():
		require.Equal(t, map[string]any{"aguiUpdated": true}, event.Patch)
		require.Empty(t, event.ResponsePayload)
		require.Empty(t, event.Content)
		receipt, outcome, err := f.client.aguiCompletedApprovalReceipt(ctx, "approval-thread", f.pending.Interrupts[0])
		require.NoError(t, err)
		require.NotNil(t, receipt)
		require.Equal(t, "real approved result", outcome.Result)
	case <-time.After(time.Second):
		t.Fatal("missing post-commit notification")
	}
}

func TestAGUIApprovalRecoveryKeysetDoesNotStarveReadyRun(t *testing.T) {
	f := newCoordinatedApprovalFixture(t, 1, false)
	ctx := recoveryContext()
	_, err := f.client.aguiApplyApproval(ctx, f.original, f.pending.Interrupts[0], approvalAnswer("approval-0", "approve"))
	require.NoError(t, err)
	// Select actual persisted identity keys before the ready parent. Human-readable
	// thread prefixes do not determine SHA-256 keyset ordering.
	selected := 0
	for i := 0; i < 50000 && selected < 200; i++ {
		runID := fmt.Sprintf("unanswered-%d", i)
		if aguistore.RunKey("owner", f.original.ConversationID, runID) >= aguistore.RunKey("owner", f.original.ConversationID, f.original.RunID) {
			continue
		}
		thread := f.original.ThreadID
		input := rawAGUI(map[string]any{"threadId": thread, "runId": runID, "messages": []any{map[string]any{"id": "user-" + runID, "role": "user", "content": "question"}}})
		run, _, err := f.store.Admit(ctx, aguistore.Admission{Principal: "owner", ThreadID: thread, RunID: runID, TurnID: runID + "-turn", Input: input})
		require.NoError(t, err)
		require.Less(t, aguistore.RunKey("owner", run.ConversationID, run.RunID), aguistore.RunKey("owner", f.original.ConversationID, f.original.RunID))
		pending := aguiPending{Interrupts: []agui.WireInterrupt{{ID: "unanswered", Reason: "elicitation"}}}
		_, err = f.store.Append(ctx, "owner", thread, runID, run.Revision, []json.RawMessage{rawAGUI(map[string]any{"type": "RUN_STARTED", "threadId": thread, "runId": runID}), rawAGUI(map[string]any{"type": "RUN_FINISHED", "threadId": thread, "runId": runID, "outcome": map[string]any{"type": "interrupt", "interrupts": pending.Interrupts}})}, &aguistore.Change{Pending: rawAGUI(pending)})
		require.NoError(t, err)
		selected++
	}
	require.Equal(t, 200, selected)

	firstPage, err := f.store.ReadApprovalRecovery(ctx, "", time.Now().UTC(), 50)
	require.NoError(t, err)
	require.Len(t, firstPage, 50)
	for _, candidate := range firstPage {
		require.NotEqual(t, "original", candidate.RunID, "ready parent must start beyond first page")
	}
	require.NoError(t, f.client.ReconcileAGUIApprovals(ctx))
	parent, err := f.store.GetRun(ctx, "owner", "approval-thread", "original")
	require.NoError(t, err)
	require.Empty(t, parent.ResumedByRunID)
	for pass := 0; pass < 5; pass++ {
		require.NoError(t, f.client.ReconcileAGUIApprovals(ctx))
		latest, err := f.store.GetRun(ctx, "owner", "approval-thread", "original")
		require.NoError(t, err)
		if latest.ResumedByRunID != "" {
			break
		}
	}
	waitApprovalSuccessor(t, f.store, f.original)
	require.Equal(t, int64(1), f.registry.calls.Load())
}

func TestAGUIApprovalSuccessorPreservesActivityEncryptedMultipartAndNullState(t *testing.T) {
	f := newCoordinatedApprovalFixture(t, 1, false)
	ctx := recoveryContext()
	answer := approvalAnswer("approval-0", "approve")
	_, err := f.client.aguiApplyApproval(ctx, f.original, f.pending.Interrupts[0], answer)
	require.NoError(t, err)
	thread, err := f.store.GetThread(ctx, "owner", "approval-thread")
	require.NoError(t, err)
	var messages []json.RawMessage
	require.NoError(t, json.Unmarshal(thread.Messages, &messages))
	messages = append(messages, json.RawMessage(`{"id":"activity","role":"activity","activityType":"agently.turn","content":{"version":"1","nativeTurnId":"approval-turn","phase":"waiting"}}`), json.RawMessage(`{"id":"opaque-reasoning","role":"reasoning","content":"","encryptedValue":"opaque-artifact"}`), json.RawMessage(`{"id":"multipart-user","role":"user","name":"","content":[{"type":"image","source":{"type":"data","value":"AA==","mimeType":"image/png"}}]}`))
	stateCommand, _, err := f.store.Admit(ctx, aguistore.Admission{Principal: "owner", ThreadID: "approval-thread", RunID: "nullable-state", Input: json.RawMessage(`{"threadId":"approval-thread","runId":"nullable-state","messages":[]}`)})
	require.NoError(t, err)
	// Existing store transaction simulates an independent state command; native
	// transcript persistence is untouched.
	thread, err = f.store.GetThread(ctx, "owner", "approval-thread")
	require.NoError(t, err)
	_, err = f.store.Append(ctx, "owner", "approval-thread", stateCommand.RunID, stateCommand.Revision, []json.RawMessage{rawAGUI(map[string]any{"type": "RUN_STARTED", "threadId": "approval-thread", "runId": stateCommand.RunID}), rawAGUI(map[string]any{"type": "RUN_FINISHED", "threadId": "approval-thread", "runId": stateCommand.RunID, "outcome": map[string]any{"type": "success"}})}, &aguistore.Change{State: json.RawMessage("null"), Messages: rawAGUI(messages), ExpectedThreadRevision: thread.Revision})
	require.NoError(t, err)
	id, remaining, err := f.client.aguiApprovalContinuation(ctx, f.store, f.original, f.pending)
	require.NoError(t, err)
	require.Empty(t, remaining)
	successor, err := f.store.GetRun(ctx, "owner", "approval-thread", id)
	require.NoError(t, err)
	require.NoError(t, agui.ValidateInput(successor.Input))
	var actual struct {
		Messages []json.RawMessage `json:"messages"`
		State    json.RawMessage   `json:"state"`
	}
	require.NoError(t, json.Unmarshal(successor.Input, &actual))
	require.Empty(t, actual.State)
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(successor.Input, &fields))
	require.NotContains(t, fields, "state")
	require.JSONEq(t, string(rawAGUI(messages)), string(rawAGUI(actual.Messages)))
	require.Contains(t, string(successor.Input), `"useServerState":true`)
	final := waitApprovalSuccessor(t, f.store, f.original)
	finalJournal, err := aguiRecoveryJournal(ctx, f.store, final)
	require.NoError(t, err)
	require.NotContains(t, string(rawAGUI(finalJournal)), "native presentation identity belongs to trusted producers")
	thread, err = f.store.GetThread(ctx, "owner", "approval-thread")
	require.NoError(t, err)
	require.JSONEq(t, "null", string(thread.State))
	require.Equal(t, int64(1), f.registry.calls.Load())
}

func TestAGUIApprovalUncertainReceiptNeverRetriesOrResumes(t *testing.T) {
	f := newCoordinatedApprovalFixture(t, 1, false)
	ctx := recoveryContext()
	interrupt := f.pending.Interrupts[0]
	answer := approvalAnswer(interrupt.ID, "approve")
	row, err := f.client.aguiApprovalRow(ctx, "approval-thread", interrupt)
	require.NoError(t, err)
	metadata := aguiApprovalMetadata(row)
	metadata["aguiDecision"], err = canonicalJSONValue(rawAGUI(answer))
	require.NoError(t, err)
	blob := []byte(rawAGUI(metadata))
	row.Metadata = &blob
	claimer := f.client.conv.(interface {
		ClaimToolApprovalDecision(context.Context, *toolapprovalqueuemodel.QueueRowView, string, string) error
	})
	require.NoError(t, claimer.ClaimToolApprovalDecision(ctx, row, "owner", "approve"))
	_, err = f.client.DecideToolApproval(ctx, &DecideToolApprovalInput{ID: interrupt.ID, Action: "approve"})
	require.ErrorContains(t, err, "uncertain")
	require.ErrorContains(t, f.client.ReconcileAGUIApprovals(ctx), "uncertain")
	require.Zero(t, f.registry.calls.Load())
	parent, err := f.store.GetRun(ctx, "owner", "approval-thread", "original")
	require.NoError(t, err)
	require.Empty(t, parent.ResumedByRunID)
	current, err := f.client.aguiApprovalRow(ctx, "approval-thread", interrupt)
	require.NoError(t, err)
	require.Equal(t, "approved", current.Status)
	require.Empty(t, aguiApprovalMetadata(current)["aguiOutcome"])
}

func TestAGUIApprovalDirectAdmissionAndInboxUseSameEffectReceipt(t *testing.T) {
	f := newCoordinatedApprovalFixture(t, 1, false)
	ctx := recoveryContext()
	answer := approvalAnswer("approval-0", "approve")
	input := rawAGUI(map[string]any{"threadId": "approval-thread", "runId": "direct-standard", "messages": []any{}, "resume": []agui.WireResumeEntry{answer}})
	_, _, err := f.store.Admit(ctx, aguistore.Admission{Principal: "owner", ThreadID: "approval-thread", RunID: "direct-standard", PriorRunID: "original", ExpectedPriorRevision: f.original.Revision, TurnID: "approval-turn", Input: input})
	require.NoError(t, err)
	out, err := f.client.DecideToolApproval(ctx, &DecideToolApprovalInput{ID: "approval-0", Action: "approve"})
	require.NoError(t, err)
	require.Equal(t, "direct-standard", out.Protocol.ContinuationRunID)
	require.NoError(t, f.client.ReconcileAGUIApprovals(ctx))
	waitApprovalSuccessor(t, f.store, f.original)
	require.Equal(t, int64(1), f.registry.calls.Load())
	runs, err := f.store.ListRunsByTurn(ctx, "owner", "approval-thread", "approval-turn")
	require.NoError(t, err)
	require.Len(t, runs, 2, "no second successor admitted")
}

func TestAGUIApprovalMixedResumeAggregatesOnlyGenuineReceiptAnswers(t *testing.T) {
	f := newCoordinatedApprovalFixture(t, 1, true)
	ctx := recoveryContext()
	_, err := f.client.DecideToolApproval(ctx, &DecideToolApprovalInput{ID: "approval-0", Action: "approve"})
	require.NoError(t, err)
	input := &agui.RunAgentInput{ThreadID: "approval-thread", RunID: "mixed-resume", Messages: []agui.Message{}, Resume: json.RawMessage(`[{"interruptId":"genuine-question","status":"resolved","payload":{"answer":"real user answer"}}]`)}
	original, pending, err := findAGUIContinuation(ctx, f.store, "owner", input, f.client)
	require.NoError(t, err)
	require.Equal(t, "original", original.RunID)
	require.NoError(t, preflightAGUIResume(ctx, nil, input, pending))
	var answers []agui.WireResumeEntry
	require.NoError(t, json.Unmarshal(input.Resume, &answers))
	require.Len(t, answers, 2)
	require.Equal(t, "genuine-question", answers[0].InterruptId)
	require.JSONEq(t, `{"answer":"real user answer"}`, string(*answers[0].Payload))
	require.Equal(t, "approval-0", answers[1].InterruptId)
	require.Equal(t, int64(1), f.registry.calls.Load())
}
