package sdk

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/viant/agently-core/protocol/agent/execution"
	"github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/service/elicitation"
	elicitationrouter "github.com/viant/agently-core/service/elicitation/mcp"
	"github.com/viant/mcp-protocol/schema"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	aguistore "github.com/viant/agently-core/app/store/agui"
	"github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/app/store/data"
	"github.com/viant/agently-core/app/store/native"
	iauth "github.com/viant/agently-core/internal/auth"
	convservice "github.com/viant/agently-core/internal/service/conversation"
	"github.com/viant/agently-core/protocol/agui"
	"github.com/viant/agently-core/runtime/aguistate"
	"github.com/viant/agently-core/runtime/clienttool"
	"github.com/viant/agently-core/runtime/streaming"
	agentsvc "github.com/viant/agently-core/service/agent"
)

type recoveryAGUIClient struct {
	*durableAGUIClient
	inspect      func(context.Context, *aguistore.Run) (*aguiRecoveredNative, error)
	cancelled    atomic.Int32
	queued       atomic.Int32
	cancelTurn   string
	cancelThread string
}

func (c *recoveryAGUIClient) aguiInspectRun(ctx context.Context, run *aguistore.Run) (*aguiRecoveredNative, error) {
	return c.inspect(ctx, run)
}
func (c *recoveryAGUIClient) CancelTurn(_ context.Context, turn string) (bool, error) {
	c.cancelTurn = turn
	c.cancelled.Add(1)
	return true, nil
}
func (c *recoveryAGUIClient) CancelQueuedTurn(_ context.Context, thread, turn string) error {
	c.cancelThread = thread
	c.cancelTurn = turn
	c.queued.Add(1)
	return nil
}
func newRecoveryAGUIClient(t *testing.T) *recoveryAGUIClient {
	t.Helper()
	ctx := context.Background()
	server, err := native.New(ctx, native.Options{WorkspaceRoot: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Shutdown(ctx)) })
	conv, err := convservice.New(ctx, server)
	require.NoError(t, err)
	owned := conversation.NewConversation()
	owned.SetId("thread")
	owned.SetCreatedByUserID("owner")
	require.NoError(t, conv.PatchConversations(ctx, owned))
	c := &recoveryAGUIClient{durableAGUIClient: &durableAGUIClient{aguiTestClient: newAGUITestClient(), store: aguistore.New(server)}}
	c.owner = "owner"
	c.query = func(context.Context, *agentsvc.QueryInput) (*agentsvc.QueryOutput, error) {
		return nil, fmt.Errorf("recovery unexpectedly repeated Query")
	}
	return c
}
func recoveryContext() context.Context {
	return iauth.WithUserInfo(context.Background(), &iauth.UserInfo{Subject: "owner"})
}
func recoveryRunning(t *testing.T, c *recoveryAGUIClient, extra ...json.RawMessage) *aguistore.Run {
	t.Helper()
	ctx := recoveryContext()
	record, _, err := c.store.Admit(ctx, aguistore.Admission{ThreadID: "thread", RunID: "external-run", Principal: "owner", TurnID: "user-message", Input: json.RawMessage(aguiChatRequest)})
	require.NoError(t, err)
	thread, err := c.store.GetThread(ctx, "owner", "thread")
	require.NoError(t, err)
	projection, err := aguistate.New(json.RawMessage(`{"preserved":true}`), json.RawMessage(`[{"id":"user-message","role":"user","content":"hello"}]`))
	require.NoError(t, err)
	writer := &aguiJournalWriter{ctx: ctx, store: c.store, run: record, projection: projection, threadRevision: thread.Revision}
	raw := []json.RawMessage{json.RawMessage(`{"type":"RUN_STARTED","threadId":"thread","runId":"external-run"}`)}
	raw = append(raw, extra...)
	require.NoError(t, writer.write(raw, nil))
	claimed, err := c.store.Claim(ctx, "owner", "thread", "external-run", writer.run.Revision, "dead-process", time.Millisecond)
	require.NoError(t, err)
	time.Sleep(5 * time.Millisecond)
	return claimed
}
func recoveryBaseline() []json.RawMessage {
	return []json.RawMessage{json.RawMessage(`{"type":"STATE_SNAPSHOT","snapshot":{"preserved":true}}`), json.RawMessage(`{"type":"MESSAGES_SNAPSHOT","messages":[{"id":"user-message","role":"user","content":"hello"}]}`)}
}
func recoveryEvents(t *testing.T, c *recoveryAGUIClient) []json.RawMessage {
	t.Helper()
	run, err := c.store.GetRun(recoveryContext(), "owner", "thread", "external-run")
	require.NoError(t, err)
	raw, err := aguiRecoveryJournal(recoveryContext(), c.store, run)
	require.NoError(t, err)
	for _, event := range raw {
		require.NoError(t, agui.ValidateEvent(event))
	}
	return raw
}
func TestAGUIRecoveryReconstructsExpiredJournalAndAuthoritativeTerminalWithoutQuery(t *testing.T) {
	c := newRecoveryAGUIClient(t)
	extra := append(recoveryBaseline(), json.RawMessage(`{"type":"TEXT_MESSAGE_START","messageId":"assistant","role":"assistant","metadata":{"opaque":"keep"}}`), json.RawMessage(`{"type":"TEXT_MESSAGE_CONTENT","messageId":"assistant","delta":"partial "}`), json.RawMessage(`{"type":"TOOL_CALL_START","toolCallId":"effect","toolCallName":"backend","parentMessageId":"assistant"}`), json.RawMessage(`{"type":"TOOL_CALL_ARGS","toolCallId":"effect","delta":"{}"}`), json.RawMessage(`{"type":"TOOL_CALL_END","toolCallId":"effect"}`))
	record := recoveryRunning(t, c, extra...)
	c.inspect = func(context.Context, *aguistore.Run) (*aguiRecoveredNative, error) {
		require.True(t, c.subscribed.Load())
		return &aguiRecoveredNative{Status: "completed", Messages: []aguistate.Object{{"id": "assistant", "role": "assistant", "content": "complete authoritative answer"}, {"id": "effect-result", "role": "tool", "toolCallId": "effect", "content": "persisted side effect result"}}}, nil
	}
	require.NoError(t, recoverAGUIDurable(recoveryContext(), c, c, c.store, record))
	require.Zero(t, c.queries.Load())
	require.Zero(t, c.resumes.Load())
	require.Zero(t, c.completions.Load())
	events := recoveryEvents(t, c)
	starts, terminals, results := 0, 0, 0
	for _, raw := range events {
		var e map[string]any
		require.NoError(t, json.Unmarshal(raw, &e))
		switch e["type"] {
		case "RUN_STARTED":
			starts++
		case "RUN_FINISHED":
			terminals++
		case "TOOL_CALL_RESULT":
			results++
		}
	}
	require.Equal(t, 1, starts)
	require.Equal(t, 1, terminals)
	require.Equal(t, 1, results, "a recovered completion is replayed once without executing the backend tool")
	stored, err := c.store.GetThread(recoveryContext(), "owner", "thread")
	require.NoError(t, err)
	require.Contains(t, string(stored.Messages), "complete authoritative answer")
	require.Contains(t, string(stored.Messages), `"opaque":"keep"`)
	require.Contains(t, string(stored.Messages), "persisted side effect result")
	require.NoError(t, recoverAGUIDurable(recoveryContext(), c, c, c.store, record))
	require.Zero(t, c.queries.Load())
}
func TestAGUIRecoveryObservesOtherProcessCompletionAndRetainsPartialText(t *testing.T) {
	c := newRecoveryAGUIClient(t)
	record := recoveryRunning(t, c, append(recoveryBaseline(), json.RawMessage(`{"type":"TEXT_MESSAGE_START","messageId":"assistant","role":"assistant"}`), json.RawMessage(`{"type":"TEXT_MESSAGE_CONTENT","messageId":"assistant","delta":"before crash "}`))...)
	var inspections atomic.Int32
	c.inspect = func(context.Context, *aguistore.Run) (*aguiRecoveredNative, error) {
		if inspections.Add(1) == 1 {
			return &aguiRecoveredNative{Status: "running"}, nil
		}
		return &aguiRecoveredNative{Status: "completed", Messages: []aguistate.Object{{"id": "assistant", "role": "assistant", "content": "before crash and after recovery"}}}, nil
	}
	ctx, cancel := context.WithTimeout(recoveryContext(), 3*time.Second)
	defer cancel()
	require.NoError(t, recoverAGUIDurable(ctx, c, c, c.store, record))
	require.GreaterOrEqual(t, inspections.Load(), int32(2))
	require.Zero(t, c.queries.Load())
	require.NotEmpty(t, recoveryEvents(t, c))
}
func TestAGUIRecoverySafePreQueryAdmissionExecutesOnceWithoutDuplicateStart(t *testing.T) {
	c := newRecoveryAGUIClient(t)
	record := recoveryRunning(t, c)
	c.inspect = func(context.Context, *aguistore.Run) (*aguiRecoveredNative, error) { return nil, nil }
	c.query = func(context.Context, *agentsvc.QueryInput) (*agentsvc.QueryOutput, error) {
		return &agentsvc.QueryOutput{Content: "recovered before dispatch"}, nil
	}
	require.NoError(t, recoverAGUIDurable(recoveryContext(), c, c, c.store, record))
	require.EqualValues(t, 1, c.queries.Load())
	raw := recoveryEvents(t, c)
	starts := 0
	for _, event := range raw {
		if strings.Contains(string(event), `"type":"RUN_STARTED"`) {
			starts++
		}
	}
	require.Equal(t, 1, starts)
	require.NoError(t, recoverAGUIDurable(recoveryContext(), c, c, c.store, record))
	require.EqualValues(t, 1, c.queries.Load())
}
func TestAGUIRecoveryDoesNotRepeatAmbiguousMissingPresetDispatch(t *testing.T) {
	c := newRecoveryAGUIClient(t)
	record := recoveryRunning(t, c, recoveryBaseline()...)
	c.inspect = func(context.Context, *aguistore.Run) (*aguiRecoveredNative, error) { return nil, nil }
	require.ErrorContains(t, recoverAGUIDurable(recoveryContext(), c, c, c.store, record), "result is uncertain")
	require.Zero(t, c.queries.Load())
	stored, err := c.store.GetRun(recoveryContext(), "owner", "thread", "external-run")
	require.NoError(t, err)
	require.Equal(t, aguistore.StatusError, stored.Status)
	require.Contains(t, string(recoveryEvents(t, c)[stored.LastSequence-1]), "execution was not repeated")
}
func TestAGUIRecoveryCancellationBindsOwnerThreadRunAndQueue(t *testing.T) {
	c := newRecoveryAGUIClient(t)
	record := recoveryRunning(t, c, recoveryBaseline()...)
	status := "queued"
	c.inspect = func(_ context.Context, actual *aguistore.Run) (*aguiRecoveredNative, error) {
		require.Equal(t, record.TurnID, actual.TurnID)
		require.Equal(t, "thread", actual.ThreadID)
		return &aguiRecoveredNative{Status: status}, nil
	}
	cancelled, err := cancelAGUIRun(recoveryContext(), c, c.store, "owner", "thread", "external-run")
	require.NoError(t, err)
	require.True(t, cancelled)
	require.EqualValues(t, 1, c.queued.Load())
	require.Equal(t, record.TurnID, c.cancelTurn)
	require.Equal(t, record.ConversationID, c.cancelThread)
	status = "running"
	cancelled, err = cancelAGUIRun(recoveryContext(), c, c.store, "owner", "thread", "external-run")
	require.NoError(t, err)
	require.True(t, cancelled)
	require.EqualValues(t, 1, c.cancelled.Load())
	for _, waitingStatus := range []string{"waiting_for_user", "canceled"} {
		status = waitingStatus
		cancelled, err = cancelAGUIRun(recoveryContext(), c, c.store, "owner", "thread", "external-run")
		require.NoError(t, err)
		require.True(t, cancelled)
		require.Equal(t, record.TurnID, c.cancelTurn)
	}
	status = "completed"
	cancelled, err = cancelAGUIRun(recoveryContext(), c, c.store, "owner", "thread", "external-run")
	require.NoError(t, err)
	require.False(t, cancelled)
	_, err = cancelAGUIRun(iauth.WithUserInfo(context.Background(), &iauth.UserInfo{Subject: "other"}), c, c.store, "owner", "thread", "external-run")
	require.Error(t, err)
	_, err = cancelAGUIRun(recoveryContext(), c, c.store, "owner", "foreign", "external-run")
	require.Error(t, err)
	require.EqualValues(t, 3, c.cancelled.Load())
	require.EqualValues(t, 1, c.queued.Load())
}
func TestAGUIRecoveryInspectorReadsNativeDatlyToolGraphAndPending(t *testing.T) {
	ctx := recoveryContext()
	server, err := native.New(ctx, native.Options{WorkspaceRoot: t.TempDir()})
	require.NoError(t, err)
	defer server.Shutdown(ctx)
	conv, err := convservice.New(ctx, server)
	require.NoError(t, err)
	conv.SetStreamPublisher(streaming.NewMemoryBus(64))
	scoped := &backendClient{data: data.NewService(server), conv: conv}
	root := conversation.NewConversation()
	root.SetId("native-thread")
	root.SetCreatedByUserID("owner")
	root.SetStatus("active")
	require.NoError(t, conv.PatchConversations(ctx, root))
	turn := conversation.NewTurn()
	turn.SetId("native-turn")
	turn.SetConversationID("native-thread")
	turn.SetStatus("waiting_for_user")
	require.NoError(t, conv.PatchTurn(ctx, turn))
	assistant := conversation.NewMessage()
	assistant.SetId("native-assistant")
	assistant.SetConversationID("native-thread")
	assistant.SetTurnID("native-turn")
	assistant.SetRole("assistant")
	assistant.SetType("text")
	assistant.SetContent("native persisted answer")
	require.NoError(t, conv.PatchMessage(ctx, assistant))
	tool := conversation.NewMessage()
	tool.SetId("native-tool")
	tool.SetConversationID("native-thread")
	tool.SetTurnID("native-turn")
	tool.SetRole("tool")
	tool.SetType("tool_call")
	tool.SetParentMessageID("native-assistant")
	require.NoError(t, conv.PatchMessage(ctx, tool))
	payload := conversation.NewPayload()
	payload.SetId("native-request")
	payload.SetKind("tool_request")
	payload.SetMimeType("application/json")
	payload.SetStorage("inline")
	payload.SetInlineBody([]byte(`{"n":9007199254740991}`))
	payload.SetSizeBytes(len(`{"n":9007199254740991}`))
	require.NoError(t, conv.PatchPayload(ctx, payload))
	call := conversation.NewToolCall()
	call.SetMessageID("native-tool")
	call.SetTurnID("native-turn")
	call.SetOpID("client-call")
	call.SetToolName("browser")
	call.SetToolKind("function")
	call.SetStatus("waiting_for_user")
	request := "native-request"
	call.RequestPayloadID = &request
	call.Has.RequestPayloadID = true
	require.NoError(t, conv.PatchToolCall(ctx, call))
	record := &aguistore.Run{ThreadID: "native-thread", ConversationID: "native-thread", TurnID: "native-turn", RunID: "wire", Principal: "owner", Input: json.RawMessage(`{"threadId":"native-thread","runId":"wire","messages":[],"tools":[{"name":"browser","description":"browser","parameters":{"type":"object"}}]}`)}
	actual, err := scoped.aguiInspectRun(ctx, record)
	require.NoError(t, err)
	require.NotNil(t, actual)
	require.Equal(t, "waiting_for_user", actual.Status)
	require.Len(t, actual.Pending.ClientTools, 1)
	require.Equal(t, "client-call", actual.Pending.ClientTools[0].ID)
	require.Equal(t, json.Number("9007199254740991"), actual.Pending.ClientTools[0].Arguments["n"])
	require.Contains(t, string(rawAGUI(actual.Messages)), "native persisted answer")
	require.Contains(t, string(rawAGUI(actual.Messages)), "native-assistant")
	scoped.goalInvoker = server
	store := aguistore.New(server)
	var aliasInput map[string]any
	require.NoError(t, json.Unmarshal(record.Input, &aliasInput))
	aliasInput["runId"] = "aliased-wire"
	aliasRecord, _, err := store.Admit(ctx, aguistore.Admission{ThreadID: record.ThreadID, RunID: "aliased-wire", TurnID: record.TurnID, Principal: "owner", Input: rawAGUI(aliasInput)})
	require.NoError(t, err)
	aliasRecord, err = store.Append(ctx, "owner", aliasRecord.ThreadID, aliasRecord.RunID, aliasRecord.Revision, []json.RawMessage{rawAGUI(map[string]any{"type": "RUN_STARTED", "threadId": aliasRecord.ThreadID, "runId": aliasRecord.RunID, "metadata": map[string]any{"agently": map[string]any{"identityVersion": "1", "nativeTurnId": aliasRecord.TurnID}}})}, nil)
	require.NoError(t, err)
	aliased, err := scoped.aguiInspectRun(ctx, aliasRecord)
	require.NoError(t, err)
	require.Equal(t, "client-call", aliased.Pending.ClientTools[0].ID)
	require.Equal(t, agui.ProtocolToolCallID("native-turn", "client-call"), aliased.Pending.ClientTools[0].ProtocolID)
	require.Contains(t, string(rawAGUI(aliased.Messages)), agui.ProtocolToolCallID("native-turn", "client-call"))
	forged := *record
	forged.ThreadID = "other-thread"
	forged.ConversationID = "other-thread"
	missing, err := scoped.aguiInspectRun(ctx, &forged)
	if err == nil {
		require.Nil(t, missing)
	}
	// Native queue presentation is durable before execution. Recovery must not
	// turn it into a public result, including while completion writes are partial.
	tool.SetContent("queued for user approval")
	tool.SetStatus("queued")
	require.NoError(t, conv.PatchMessage(ctx, tool))
	call.SetStatus("completed")
	require.NoError(t, conv.PatchToolCall(ctx, call))
	queued, err := scoped.aguiInspectRun(ctx, aliasRecord)
	require.NoError(t, err)
	require.NotContains(t, string(rawAGUI(queued.Messages)), "queued for user approval")
	require.Contains(t, string(rawAGUI(queued.Messages)), agui.ProtocolToolCallID("native-turn", "client-call"), "original call remains visible")
	tool.SetContent("actual execution result")
	tool.SetStatus("completed")
	require.NoError(t, conv.PatchMessage(ctx, tool))
	completed, err := scoped.aguiInspectRun(ctx, aliasRecord)
	require.NoError(t, err)
	require.Contains(t, string(rawAGUI(completed.Messages)), "actual execution result")
}

func TestAGUIRecoveryResumesAcceptedClientToolContinuationWithoutFreshQuery(t *testing.T) {
	c := newRecoveryAGUIClient(t)
	call := clienttool.PendingCall{ID: "client", Name: "browser", Arguments: map[string]interface{}{}, ConversationID: "thread", TurnID: "user-message", ToolMessageID: "native-tool", AssistantMessageID: "assistant"}
	before := append(recoveryBaseline(), json.RawMessage(`{"type":"TEXT_MESSAGE_START","messageId":"assistant","role":"assistant"}`), json.RawMessage(`{"type":"TOOL_CALL_START","toolCallId":"client","toolCallName":"browser","parentMessageId":"assistant"}`), json.RawMessage(`{"type":"TOOL_CALL_ARGS","toolCallId":"client","delta":"{}"}`), json.RawMessage(`{"type":"TOOL_CALL_END","toolCallId":"client"}`))
	old := recoveryRunning(t, c, before...)
	c.inspect = func(context.Context, *aguistore.Run) (*aguiRecoveredNative, error) {
		return &aguiRecoveredNative{Status: "waiting_for_user", Pending: aguiPending{ClientTools: []clienttool.PendingCall{call}}}, nil
	}
	require.NoError(t, recoverAGUIDurable(recoveryContext(), c, c, c.store, old))
	prior, err := c.store.GetRun(recoveryContext(), "owner", "thread", "external-run")
	require.NoError(t, err)
	body := json.RawMessage(`{"threadId":"thread","runId":"continued","messages":[{"id":"client-result","role":"tool","toolCallId":"client","content":"browser result"}],"tools":[{"name":"browser","description":"client tool","parameters":{"type":"object"}}]}`)
	record, fresh, err := c.store.Admit(recoveryContext(), aguistore.Admission{ThreadID: "thread", RunID: "continued", Principal: "owner", PriorRunID: prior.RunID, ExpectedPriorRevision: prior.Revision, Input: body})
	require.NoError(t, err)
	require.True(t, fresh)
	thread, err := c.store.GetThread(recoveryContext(), "owner", "thread")
	require.NoError(t, err)
	projection, err := aguistate.New(thread.State, thread.Messages)
	require.NoError(t, err)
	writer := &aguiJournalWriter{ctx: recoveryContext(), store: c.store, run: record, projection: projection, threadRevision: thread.Revision}
	require.NoError(t, writer.write([]json.RawMessage{rawAGUI(map[string]any{"type": "RUN_STARTED", "threadId": "thread", "runId": "continued"}), rawAGUI(map[string]any{"type": "MESSAGES_SNAPSHOT", "messages": projection.Messages})}, nil))
	record, err = c.store.Claim(recoveryContext(), "owner", "thread", "continued", writer.run.Revision, "dead-resumer", time.Millisecond)
	require.NoError(t, err)
	time.Sleep(5 * time.Millisecond)
	// Simulates death after a native result was already persisted, before the
	// execution loop resumed. The native completion method is idempotent.
	c.inspect = func(context.Context, *aguistore.Run) (*aguiRecoveredNative, error) {
		return &aguiRecoveredNative{Status: "waiting_for_user"}, nil
	}
	require.NoError(t, recoverAGUIDurable(recoveryContext(), c, c, c.store, record))
	require.Zero(t, c.queries.Load())
	require.EqualValues(t, 1, c.resumes.Load())
	require.EqualValues(t, 1, c.completions.Load())
	stored, err := c.store.GetRun(recoveryContext(), "owner", "thread", "continued")
	require.NoError(t, err)
	require.Equal(t, aguistore.StatusFinished, stored.Status)
	require.NoError(t, recoverAGUIDurable(recoveryContext(), c, c, c.store, record))
	require.EqualValues(t, 1, c.resumes.Load())
	require.EqualValues(t, 1, c.completions.Load())
	raw, err := aguiRecoveryJournal(recoveryContext(), c.store, stored)
	require.NoError(t, err)
	for _, event := range raw {
		require.NoError(t, agui.ValidateEvent(event))
	}
}

func TestAGUIRecoveryContinuesExistingMemoryBusStreamWithoutRepeatedPrefix(t *testing.T) {
	c := newRecoveryAGUIClient(t)
	record := recoveryRunning(t, c, append(recoveryBaseline(), json.RawMessage(`{"type":"TEXT_MESSAGE_START","messageId":"assistant","role":"assistant"}`), json.RawMessage(`{"type":"TEXT_MESSAGE_CONTENT","messageId":"assistant","delta":"before crash "}`))...)
	var done atomic.Bool
	c.inspect = func(context.Context, *aguistore.Run) (*aguiRecoveredNative, error) {
		if done.Load() {
			return &aguiRecoveredNative{Status: "completed", Messages: []aguistate.Object{{"id": "assistant", "role": "assistant", "content": "before crash live suffix"}}}, nil
		}
		return &aguiRecoveredNative{Status: "running"}, nil
	}
	ctx, cancel := context.WithTimeout(recoveryContext(), 3*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- recoverAGUIDurable(ctx, c, c, c.store, record) }()
	require.Eventually(t, func() bool { return c.subscribed.Load() }, time.Second, time.Millisecond)
	offset := len("before crash ")
	require.NoError(t, c.bus.Publish(ctx, &streaming.Event{Type: streaming.EventTypeTextDelta, ConversationID: "thread", TurnID: "user-message", MessageID: "assistant", Content: "live suffix", ContentOffset: &offset}))
	require.Eventually(t, func() bool {
		thread, err := c.store.GetThread(ctx, "owner", "thread")
		return err == nil && strings.Contains(string(thread.Messages), "before crash live suffix")
	}, time.Second, 5*time.Millisecond)
	done.Store(true)
	require.NoError(t, c.bus.Publish(ctx, &streaming.Event{Type: streaming.EventTypeTurnCompleted, ConversationID: "thread", TurnID: "user-message"}))
	require.NoError(t, <-result)
	require.Zero(t, c.queries.Load())
	raw := recoveryEvents(t, c)
	starts := 0
	for _, event := range raw {
		if strings.Contains(string(event), `"type":"TEXT_MESSAGE_START"`) {
			starts++
		}
	}
	require.Equal(t, 1, starts)
}

type humanRecoveryAGUIClient struct {
	*recoveryAGUIClient
	checked *backendClient
}

func (c *humanRecoveryAGUIClient) aguiApplyInterrupt(ctx context.Context, run *aguistore.Run, interrupt agui.WireInterrupt, answer agui.WireResumeEntry) (aguiInterruptDisposition, error) {
	return c.checked.aguiApplyInterrupt(ctx, run, interrupt, answer)
}
func TestAGUIRecoveryReconcilesDurableHumanAnswerCheckpoints(t *testing.T) {
	for _, checkpoint := range []string{"before_receipt", "after_receipt_before_resume", "conflicting_receipt", "live_waiter_before_receipt"} {
		t.Run(checkpoint, func(t *testing.T) {
			ctx := recoveryContext()
			base := newRecoveryAGUIClient(t)
			server, err := native.New(ctx, native.Options{WorkspaceRoot: t.TempDir()})
			require.NoError(t, err)
			defer server.Shutdown(ctx)
			base.store = aguistore.New(server)
			conv, err := convservice.New(ctx, server)
			require.NoError(t, err)
			root := conversation.NewConversation()
			root.SetId("thread")
			root.SetCreatedByUserID("owner")
			root.SetStatus("active")
			require.NoError(t, conv.PatchConversations(ctx, root))
			turn := conversation.NewTurn()
			turn.SetId("user-message")
			turn.SetConversationID("thread")
			turn.SetStatus("waiting_for_user")
			require.NoError(t, conv.PatchTurn(ctx, turn))
			router := elicitationrouter.New()
			wake := make(chan *schema.ElicitResult, 1)
			if checkpoint == "live_waiter_before_receipt" {
				router.RegisterByElicitationID("thread", "ask", wake)
			}
			svc := elicitation.New(conv, nil, router, elicitation.NoopAwaiterFactory())
			_, err = svc.Record(ctx, &requestctx.TurnMeta{ConversationID: "thread", TurnID: "user-message"}, "assistant", &execution.Elicitation{ElicitRequestParams: schema.ElicitRequestParams{Message: "Choose", ElicitationId: "ask"}})
			require.NoError(t, err)
			c := &humanRecoveryAGUIClient{recoveryAGUIClient: base, checked: &backendClient{elicSvc: svc}}
			pending := aguiPending{Interrupts: []agui.WireInterrupt{{ID: "ask", Reason: "elicitation"}}}
			old := recoveryRunning(t, base, recoveryBaseline()...)
			base.inspect = func(context.Context, *aguistore.Run) (*aguiRecoveredNative, error) {
				return &aguiRecoveredNative{Status: "waiting_for_user", Pending: pending}, nil
			}
			require.NoError(t, recoverAGUIDurable(ctx, c, c, c.store, old))
			prior, err := c.store.GetRun(ctx, "owner", "thread", "external-run")
			require.NoError(t, err)
			input := json.RawMessage(`{"threadId":"thread","runId":"continued-human","messages":[],"resume":[{"interruptId":"ask","status":"resolved","payload":{"color":"red"}}]}`)
			record, _, err := c.store.Admit(ctx, aguistore.Admission{ThreadID: "thread", RunID: "continued-human", Principal: "owner", TurnID: "user-message", PriorRunID: prior.RunID, ExpectedPriorRevision: prior.Revision, Input: input})
			require.NoError(t, err)
			thread, err := c.store.GetThread(ctx, "owner", "thread")
			require.NoError(t, err)
			projection, err := aguistate.New(thread.State, thread.Messages)
			require.NoError(t, err)
			writer := &aguiJournalWriter{ctx: ctx, store: c.store, run: record, projection: projection, threadRevision: thread.Revision}
			require.NoError(t, writer.write([]json.RawMessage{rawAGUI(map[string]any{"type": "RUN_STARTED", "threadId": "thread", "runId": record.RunID}), rawAGUI(map[string]any{"type": "MESSAGES_SNAPSHOT", "messages": projection.Messages})}, nil))
			if checkpoint != "before_receipt" && checkpoint != "live_waiter_before_receipt" {
				color := "red"
				if checkpoint == "conflicting_receipt" {
					color = "blue"
				}
				_, err = svc.ResolveChecked(ctx, "thread", "ask", "accept", map[string]interface{}{"color": color}, "")
				require.NoError(t, err)
			}
			record, err = c.store.Claim(ctx, "owner", "thread", record.RunID, writer.run.Revision, "dead-human-resumer", time.Millisecond)
			require.NoError(t, err)
			time.Sleep(5 * time.Millisecond)
			base.inspect = func(context.Context, *aguistore.Run) (*aguiRecoveredNative, error) {
				if checkpoint == "live_waiter_before_receipt" {
					if len(wake) > 0 {
						return &aguiRecoveredNative{Status: "completed"}, nil
					}
					return &aguiRecoveredNative{Status: "running"}, nil
				}
				return &aguiRecoveredNative{Status: "waiting_for_user"}, nil
			}
			err = recoverAGUIDurable(ctx, c, c, c.store, record)
			if checkpoint == "conflicting_receipt" {
				require.ErrorContains(t, err, "different answer")
				require.Zero(t, c.resumes.Load())
			} else {
				require.NoError(t, err)
				if checkpoint == "live_waiter_before_receipt" {
					require.Zero(t, c.resumes.Load())
					require.Len(t, wake, 1)
				} else {
					require.EqualValues(t, 1, c.resumes.Load())
				}
				receipt, receiptErr := svc.InspectResolution(ctx, "thread", "ask", "accept", map[string]interface{}{"color": "red"}, "")
				require.NoError(t, receiptErr)
				require.NotNil(t, receipt)
				require.NoError(t, recoverAGUIDurable(ctx, c, c, c.store, record))
				if checkpoint == "live_waiter_before_receipt" {
					require.Zero(t, c.resumes.Load())
					require.Len(t, wake, 1)
				} else {
					require.EqualValues(t, 1, c.resumes.Load())
				}
			}
			require.Zero(t, c.queries.Load())
		})
	}
}

func TestAGUIRecoveryTerminalOutcomeDoesNotMixPendingToolIdsIntoInterrupts(t *testing.T) {
	for _, status := range []string{"waiting_for_user", "cancelled"} {
		t.Run(status, func(t *testing.T) {
			c := newRecoveryAGUIClient(t)
			record := recoveryRunning(t, c, recoveryBaseline()...)
			pending := aguiPending{Interrupts: []agui.WireInterrupt{{ID: "ask", Reason: "elicitation"}}, ClientTools: []clienttool.PendingCall{{ID: "nested", ProtocolID: "public-nested", Name: "browser", Arguments: map[string]interface{}{}, ConversationID: "thread", TurnID: "user-message"}}, Dependencies: []clienttool.Dependency{{}}}
			c.inspect = func(context.Context, *aguistore.Run) (*aguiRecoveredNative, error) {
				return &aguiRecoveredNative{Status: status, Pending: pending}, nil
			}
			require.NoError(t, recoverAGUIDurable(recoveryContext(), c, c, c.store, record))
			events := recoveryEvents(t, c)
			var end map[string]any
			require.NoError(t, json.Unmarshal(events[len(events)-1], &end))
			outcome := end["outcome"].(map[string]any)
			require.NotContains(t, outcome, "pendingToolCallIds")
			if status == "cancelled" {
				require.Equal(t, "cancelled", outcome["type"])
				require.NotContains(t, outcome, "interrupts")
			} else {
				require.Equal(t, "interrupt", outcome["type"])
				require.Len(t, outcome["interrupts"], 2)
			}
		})
	}
}
