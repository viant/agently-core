package sdk

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	aguistore "github.com/viant/agently-core/app/store/agui"
	"github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/app/store/data"
	"github.com/viant/agently-core/app/store/native"
	convservice "github.com/viant/agently-core/internal/service/conversation"
	toolapprovalqueuemodel "github.com/viant/agently-core/model/toolapprovalqueue"
	"github.com/viant/agently-core/protocol/agui"
	"github.com/viant/agently-core/runtime/aguistate"
	"github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/runtime/streaming"
	agentsvc "github.com/viant/agently-core/service/agent"
)

type datlyObservedClient struct {
	*durableAGUIClient
	native *backendClient
	conv   *convservice.Service
}

func (c *datlyObservedClient) aguiInspectRun(ctx context.Context, record *aguistore.Run) (*aguiRecoveredNative, error) {
	return c.native.aguiInspectRun(ctx, record)
}
func newDatlyObservedClient(t *testing.T, buffer int) *datlyObservedClient {
	t.Helper()
	ctx := recoveryContext()
	server, err := native.New(ctx, native.Options{WorkspaceRoot: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Shutdown(ctx)) })
	conv, err := convservice.New(ctx, server)
	require.NoError(t, err)
	c := &datlyObservedClient{durableAGUIClient: &durableAGUIClient{aguiTestClient: newAGUITestClient(), store: aguistore.New(server)}, conv: conv}
	c.bus = streaming.NewMemoryBus(buffer)
	c.owner = "owner"
	conv.SetStreamPublisher(c.bus)
	c.native = &backendClient{data: data.NewService(server), conv: conv, goalInvoker: server}
	root := conversation.NewConversation()
	root.SetId("thread")
	root.SetCreatedByUserID("owner")
	root.SetStatus("active")
	require.NoError(t, conv.PatchConversations(ctx, root))
	return c
}
func nativeTestTurn(ctx context.Context, c *datlyObservedClient, id, status string) error {
	turn := conversation.NewTurn()
	turn.SetId(id)
	turn.SetConversationID("thread")
	turn.SetStatus(status)
	return c.conv.PatchTurn(ctx, turn)
}
func nativeTestMessage(ctx context.Context, c *datlyObservedClient, turnID, id, role, content string) error {
	message := conversation.NewMessage()
	message.SetId(id)
	message.SetConversationID("thread")
	message.SetTurnID(turnID)
	message.SetRole(role)
	message.SetType("text")
	message.SetContent(content)
	if role == "tool" {
		message.SetType("tool_call")
		message.SetParentMessageID("assistant")
	}
	return c.conv.PatchMessage(ctx, message)
}
func observerAdmission(t *testing.T, c *datlyObservedClient, input *agui.RunAgentInput) (*aguistore.Run, *agentsvc.QueryInput) {
	t.Helper()
	input.ThreadID = "thread"
	input.RunID = "external-run"
	record, _, err := c.store.Admit(recoveryContext(), aguistore.Admission{ThreadID: input.ThreadID, RunID: input.RunID, TurnID: "native-turn", Principal: "owner", Input: rawAGUI(input)})
	require.NoError(t, err)
	query, err := queryForAGUI(input, "owner", record.TurnID, nil, nil)
	require.NoError(t, err)
	return record, query
}
func observerInput() *agui.RunAgentInput {
	return &agui.RunAgentInput{Messages: []agui.Message{{ID: "user-message", Role: "user", Content: json.RawMessage(`"hello"`)}}}
}

func TestAGUIDurableQueuedCanonicalHandoffReadsDatlyClientToolsAndApproval(t *testing.T) {
	for _, approval := range []bool{false, true} {
		t.Run(fmt.Sprint("approval=", approval), func(t *testing.T) {
			c := newDatlyObservedClient(t, 64)
			input := observerInput()
			input.Tools = []agui.Tool{{Name: "browser", Description: "frontend", Parameters: json.RawMessage(`{"type":"object"}`)}}
			record, query := observerAdmission(t, c, input)
			release := make(chan struct{})
			persisted := make(chan error, 1)
			c.query = func(ctx context.Context, q *agentsvc.QueryInput) (*agentsvc.QueryOutput, error) {
				if err := nativeTestTurn(ctx, c, q.MessageID, "queued"); err != nil {
					return nil, err
				}
				if err := c.bus.Publish(ctx, &streaming.Event{Type: streaming.EventTypeTurnQueued, ConversationID: q.ConversationID, TurnID: q.MessageID}); err != nil {
					return nil, err
				}
				// A model boundary is not a queued turn's canonical outcome.
				if err := c.bus.Publish(ctx, &streaming.Event{Type: streaming.EventTypeTurnCompleted, ConversationID: q.ConversationID, TurnID: q.MessageID, Status: "tool_calls"}); err != nil {
					return nil, err
				}
				go func() { <-release; persisted <- persistQueuedHandoff(ctx, c, q.MessageID, approval) }()
				return &agentsvc.QueryOutput{}, nil
			}
			done := make(chan error, 1)
			go func() {
				done <- runAGUIDurable(recoveryContext(), c, c, c.store, record, input, query, aguiPending{}, nil)
			}()
			require.Eventually(t, func() bool { return c.queries.Load() == 1 }, time.Second, time.Millisecond)
			select {
			case err := <-done:
				t.Fatalf("queued observer finished before execution: %v", err)
			case <-time.After(30 * time.Millisecond):
			}
			close(release)
			require.NoError(t, <-persisted)
			// waiting_for_user emits no terminal native turn event. Polling must detect
			// the durable handoff rather than leave a queued protocol run open forever.
			select {
			case err := <-done:
				require.NoError(t, err)
			case <-time.After(4 * time.Second):
				t.Fatal("queued handoff never finished")
			}
			actual, err := c.store.GetRun(recoveryContext(), "owner", "thread", "external-run")
			require.NoError(t, err)
			require.Equal(t, aguistore.StatusInterrupted, actual.Status)
			journal, err := aguiRecoveryJournal(recoveryContext(), c.store, actual)
			require.NoError(t, err)
			terminal := string(journal[len(journal)-1])
			alias := agui.ProtocolToolCallID(record.TurnID, "call")
			if approval {
				require.Contains(t, terminal, `"reason":"approval"`)
				require.NotContains(t, terminal, "pendingToolCallIds")
			} else {
				require.Contains(t, terminal, alias)
				require.Contains(t, terminal, "pendingToolCallIds")
			}
			require.EqualValues(t, 1, c.queries.Load())
		})
	}
}
func persistQueuedHandoff(ctx context.Context, c *datlyObservedClient, turnID string, approval bool) error {
	if err := nativeTestMessage(ctx, c, turnID, "assistant", "assistant", ""); err != nil {
		return err
	}
	if err := nativeTestMessage(ctx, c, turnID, "tool", "tool", ""); err != nil {
		return err
	}
	payload := conversation.NewPayload()
	payload.SetId("args")
	payload.SetKind("tool_request")
	payload.SetMimeType("application/json")
	payload.SetStorage("inline")
	payload.SetInlineBody([]byte(`{"n":1}`))
	payload.SetSizeBytes(7)
	if err := c.conv.PatchPayload(ctx, payload); err != nil {
		return err
	}
	call := conversation.NewToolCall()
	call.SetMessageID("tool")
	call.SetTurnID(turnID)
	call.SetOpID("call")
	call.SetToolName("browser")
	call.SetToolKind("function")
	call.SetStatus("waiting_for_user")
	request := "args"
	call.RequestPayloadID = &request
	call.Has.RequestPayloadID = true
	if err := c.conv.PatchToolCall(ctx, call); err != nil {
		return err
	}
	if approval {
		queue := &toolapprovalqueuemodel.ToolApprovalQueue{Has: &toolapprovalqueuemodel.ToolApprovalQueueHas{}}
		queue.SetId("approval")
		queue.SetUserId("owner")
		queue.SetToolName("browser")
		queue.SetArguments([]byte(`{"n":1}`))
		queue.SetStatus("pending")
		queue.SetConversationId("thread")
		queue.SetTurnId(turnID)
		queue.SetMessageId("assistant")
		queue.SetMetadata([]byte(`{"opId":"call"}`))
		if err := c.conv.PatchToolApprovalQueue(ctx, queue); err != nil {
			return err
		}
	}
	return nativeTestTurn(ctx, c, turnID, "waiting_for_user")
}

type shortFirstLeaseStore struct {
	aguistore.Store
	claims atomic.Int32
}

func (s *shortFirstLeaseStore) Claim(ctx context.Context, principal, thread, run string, revision int64, owner string, ttl time.Duration) (*aguistore.Run, error) {
	if s.claims.Add(1) == 1 {
		ttl = 2 * time.Second
	}
	return s.Store.Claim(ctx, principal, thread, run, revision, owner, ttl)
}
func TestAGUIDurableBusOverflowDetachesAndRecoversPersistedNativeWithoutQuery(t *testing.T) {
	c := newDatlyObservedClient(t, 1)
	c.store = &shortFirstLeaseStore{Store: c.store}
	input := observerInput()
	record, query := observerAdmission(t, c, input)
	release := make(chan struct{})
	nativeDone := make(chan error, 1)
	nativeCancelled := atomic.Bool{}
	c.query = func(ctx context.Context, q *agentsvc.QueryInput) (*agentsvc.QueryOutput, error) {
		if err := nativeTestTurn(ctx, c, q.MessageID, "running"); err != nil {
			return nil, err
		}
		for i := 0; i < 10000; i++ {
			_ = c.bus.Publish(ctx, &streaming.Event{Type: streaming.EventTypeTextDelta, ConversationID: q.ConversationID, TurnID: q.MessageID, AssistantMessageID: "assistant", Content: "x"})
		}
		select {
		case <-ctx.Done():
			nativeCancelled.Store(true)
			nativeDone <- ctx.Err()
			return nil, ctx.Err()
		case <-release:
		}
		if err := nativeTestMessage(ctx, c, q.MessageID, "assistant", "assistant", "authoritative recovered answer"); err != nil {
			nativeDone <- err
			return nil, err
		}
		err := nativeTestTurn(ctx, c, q.MessageID, "succeeded")
		nativeDone <- err
		return &agentsvc.QueryOutput{Content: "authoritative recovered answer"}, err
	}
	err := runAGUIDurable(recoveryContext(), c, c, c.store, record, input, query, aguiPending{}, nil)
	require.ErrorIs(t, err, errAGUIObserverLost)
	actual, err := c.store.GetRun(recoveryContext(), "owner", "thread", "external-run")
	require.NoError(t, err)
	require.Equal(t, aguistore.StatusRunning, actual.Status)
	journal, err := aguiRecoveryJournal(recoveryContext(), c.store, actual)
	require.NoError(t, err)
	for _, event := range journal {
		require.NotContains(t, string(event), "RUN_ERROR")
	}
	require.False(t, nativeCancelled.Load())
	close(release)
	require.NoError(t, <-nativeDone)
	require.Eventually(t, func() bool {
		r, e := c.store.GetRun(recoveryContext(), "owner", "thread", "external-run")
		return e == nil && r.LeaseUntil != nil && !r.LeaseUntil.After(time.Now())
	}, 3*time.Second, 5*time.Millisecond)
	actual, err = c.store.GetRun(recoveryContext(), "owner", "thread", "external-run")
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(recoveryContext(), 3*time.Second)
	defer cancel()
	require.NoError(t, recoverAGUIDurable(ctx, c, c, c.store, actual))
	actual, err = c.store.GetRun(ctx, "owner", "thread", "external-run")
	require.NoError(t, err)
	require.Equal(t, aguistore.StatusFinished, actual.Status)
	journal, err = aguiRecoveryJournal(ctx, c.store, actual)
	require.NoError(t, err)
	var starts, ends int
	for _, raw := range journal {
		var event struct {
			Type string `json:"type"`
		}
		require.NoError(t, json.Unmarshal(raw, &event))
		if event.Type == "RUN_STARTED" {
			starts++
		}
		if event.Type == "RUN_FINISHED" {
			ends++
		}
		require.NotContains(t, string(raw), "RUN_ERROR")
	}
	require.Equal(t, 1, starts)
	require.Equal(t, 1, ends)
	require.EqualValues(t, 1, c.queries.Load())
	require.False(t, nativeCancelled.Load())
	thread, err := c.store.GetThread(ctx, "owner", "thread")
	require.NoError(t, err)
	require.Contains(t, string(thread.Messages), "authoritative recovered answer")
	require.NotContains(t, string(thread.Messages), "runtime stream closed")
}

func TestAGUIRecoveryBusOverflowKeepsJournalRecoverable(t *testing.T) {
	c := newDatlyObservedClient(t, 1)
	ctx := recoveryContext()
	input := observerInput()
	record, _ := observerAdmission(t, c, input)
	require.NoError(t, nativeTestTurn(ctx, c, record.TurnID, "running"))
	projection, err := aguistate.New(nil, json.RawMessage(`[{"id":"user-message","role":"user","content":"hello"}]`))
	require.NoError(t, err)
	writer := &aguiJournalWriter{ctx: ctx, client: c, store: c.store, run: record, projection: projection}
	translator := agui.NewTranslator(record.ThreadID, record.RunID)
	require.NoError(t, translator.SetNativeIdentity(record.TurnID))
	require.NoError(t, writer.write(encodeAGUIEvents(translator.Start()), nil))
	require.NoError(t, writer.write([]json.RawMessage{rawAGUI(map[string]any{"type": "MESSAGES_SNAPSHOT", "messages": projection.Messages})}, nil))
	c.store = &shortFirstLeaseStore{Store: c.store}
	c.query = func(context.Context, *agentsvc.QueryInput) (*agentsvc.QueryOutput, error) {
		return nil, fmt.Errorf("recovery must never call Query")
	}
	done := make(chan error, 1)
	go func() { done <- recoverAGUIDurable(ctx, c, c, c.store, writer.run) }()
	require.Eventually(t, func() bool { return c.subscribed.Load() }, time.Second, time.Millisecond)
	for i := 0; i < 10000; i++ {
		require.NoError(t, c.bus.Publish(ctx, &streaming.Event{Type: streaming.EventTypeTextDelta, ConversationID: record.ThreadID, TurnID: record.TurnID, AssistantMessageID: "assistant", Content: "x"}))
	}
	select {
	case err := <-done:
		require.ErrorIs(t, err, errAGUIObserverLost)
	case <-time.After(3 * time.Second):
		t.Fatal("recovery did not detach on overflow")
	}
	actual, err := c.store.GetRun(ctx, "owner", record.ThreadID, record.RunID)
	require.NoError(t, err)
	require.Equal(t, aguistore.StatusRunning, actual.Status)
	journal, err := aguiRecoveryJournal(ctx, c.store, actual)
	require.NoError(t, err)
	for _, event := range journal {
		require.NotContains(t, string(event), "RUN_ERROR")
	}
	require.NoError(t, nativeTestMessage(ctx, c, record.TurnID, "assistant", "assistant", "saved after observer overflow"))
	require.NoError(t, nativeTestTurn(ctx, c, record.TurnID, "succeeded"))
	require.Eventually(t, func() bool { return actual.LeaseUntil != nil && !actual.LeaseUntil.After(time.Now()) }, 3*time.Second, 5*time.Millisecond)
	require.NoError(t, recoverAGUIDurable(ctx, c, c, c.store, actual))
	actual, err = c.store.GetRun(ctx, "owner", record.ThreadID, record.RunID)
	require.NoError(t, err)
	require.Equal(t, aguistore.StatusFinished, actual.Status)
	require.Zero(t, c.queries.Load())
}

func TestAGUIDurableRealFailureClosesOpenLanesWithActualAttribution(t *testing.T) {
	c := newDatlyObservedClient(t, 64)
	input := observerInput()
	record, query := observerAdmission(t, c, input)
	c.query = func(ctx context.Context, q *agentsvc.QueryInput) (*agentsvc.QueryOutput, error) {
		if err := c.bus.Publish(ctx, &streaming.Event{Type: streaming.EventTypeTextDelta, ConversationID: q.ConversationID, TurnID: q.MessageID, AssistantMessageID: "assistant", Content: "partial"}); err != nil {
			return nil, err
		}
		if err := c.bus.Publish(ctx, &streaming.Event{Type: streaming.EventTypeToolCallDelta, ConversationID: q.ConversationID, TurnID: q.MessageID, AssistantMessageID: "assistant", ToolCallID: "call", ToolName: "lookup", Content: `{"q":`}); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("actual native execution failure")
	}
	err := runAGUIDurable(recoveryContext(), c, c, c.store, record, input, query, aguiPending{}, nil)
	require.ErrorContains(t, err, "actual native execution failure")
	actual, err := c.store.GetRun(recoveryContext(), "owner", record.ThreadID, record.RunID)
	require.NoError(t, err)
	require.Equal(t, aguistore.StatusError, actual.Status)
	journal, err := aguiRecoveryJournal(recoveryContext(), c.store, actual)
	require.NoError(t, err)
	var closedText, closedTool int
	for _, raw := range journal {
		var event struct {
			Type  string  `json:"type"`
			Owner *string `json:"subagentRunId"`
		}
		require.NoError(t, json.Unmarshal(raw, &event))
		if event.Type == "TEXT_MESSAGE_END" {
			closedText++
			require.Nil(t, event.Owner)
		}
		if event.Type == "TOOL_CALL_END" {
			closedTool++
			require.Nil(t, event.Owner)
		}
	}
	require.Equal(t, 1, closedText)
	require.Equal(t, 1, closedTool)
	require.Contains(t, string(journal[len(journal)-1]), "RUN_ERROR")
}

func TestAGUIDurableChildOverflowDoesNotBecomeNativeBusinessFailure(t *testing.T) {
	c := newDatlyObservedClient(t, 1)
	input := observerInput()
	record, query := observerAdmission(t, c, input)
	release := make(chan struct{})
	nativeDone := make(chan error, 1)
	c.query = func(ctx context.Context, q *agentsvc.QueryInput) (*agentsvc.QueryOutput, error) {
		inv := requestctx.Invocation{ID: "child-run", ConversationID: "child-thread", TurnID: "child-turn", Name: "child", ParentConversationID: q.ConversationID, ParentTurnID: q.MessageID}
		childCtx, err := requestctx.ObserveInvocation(ctx, inv)
		if err != nil {
			nativeDone <- err
			return nil, err
		}
		for i := 0; i < 10000; i++ {
			_ = c.bus.Publish(childCtx, &streaming.Event{Type: streaming.EventTypeTextDelta, ConversationID: inv.ConversationID, TurnID: inv.TurnID, AssistantMessageID: "child-answer", Content: "x"})
		}
		<-release
		err = requestctx.NotifyInvocationReturned(childCtx, requestctx.InvocationResult{Invocation: inv, NativeStatus: "succeeded", Content: "successful child"})
		nativeDone <- err
		return &agentsvc.QueryOutput{Content: "successful root"}, err
	}
	err := runAGUIDurable(recoveryContext(), c, c, c.store, record, input, query, aguiPending{}, nil)
	require.ErrorIs(t, err, errAGUIObserverLost)
	close(release)
	require.NoError(t, <-nativeDone, "observer loss must not alter successful invocation outcome")
	actual, err := c.store.GetRun(recoveryContext(), "owner", record.ThreadID, record.RunID)
	require.NoError(t, err)
	require.Equal(t, aguistore.StatusRunning, actual.Status)
	journal, err := aguiRecoveryJournal(recoveryContext(), c.store, actual)
	require.NoError(t, err)
	for _, raw := range journal {
		require.NotContains(t, string(raw), "RUN_ERROR")
	}
	require.EqualValues(t, 1, c.queries.Load())
}

func TestAGUIDetachedOverflowKeepsNativeContextAndJournalIndependent(t *testing.T) {
	c := newDatlyObservedClient(t, 1)
	input := observerInput()
	record, _ := observerAdmission(t, c, input)
	c.store = &shortFirstLeaseStore{Store: c.store}
	parent := newAGUIInvocationObserver(recoveryContext(), c, record.ThreadID, record.TurnID)
	parent.runtime, parent.store, parent.run = c, c.store, record
	inv := requestctx.Invocation{Detached: true, ExecutionMode: "detach", ID: "detached-run", ConversationID: "detached-thread", TurnID: "detached-turn", Name: "detached", ParentConversationID: record.ThreadID, ParentTurnID: record.TurnID}
	childShell := conversation.NewConversation()
	childShell.SetId(inv.ConversationID)
	childShell.SetCreatedByUserID("owner")
	require.NoError(t, c.conv.PatchConversations(recoveryContext(), childShell))
	nativeCtx, err := startAGUIDetached(recoveryContext(), parent, inv)
	require.NoError(t, err)
	observer := requestctx.InvocationObserverFromContext(nativeCtx).(*aguiDetachedObserver)
	for i := 0; i < 10000; i++ {
		require.NoError(t, c.bus.Publish(nativeCtx, &streaming.Event{Type: streaming.EventTypeTextDelta, ConversationID: inv.ConversationID, TurnID: inv.TurnID, AssistantMessageID: "detached-answer", Content: "x"}))
	}
	select {
	case <-observer.ctx.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("detached observer did not exit after overflow")
	}
	require.NoError(t, nativeCtx.Err(), "observer cancellation is not native cancellation")
	require.NoError(t, requestctx.NotifyInvocationReturned(nativeCtx, requestctx.InvocationResult{Invocation: inv, NativeStatus: "succeeded", Content: "finished after detach"}))
	actual, err := c.store.GetRun(recoveryContext(), "owner", inv.ConversationID, inv.ID)
	require.NoError(t, err)
	require.Equal(t, aguistore.StatusRunning, actual.Status)
	journal, err := aguiRecoveryJournal(recoveryContext(), c.store, actual)
	require.NoError(t, err)
	for _, raw := range journal {
		require.NotContains(t, string(raw), "RUN_ERROR")
	}
	require.Equal(t, record.RunID, actual.ParentRunID)
}
