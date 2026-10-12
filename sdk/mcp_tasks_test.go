package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/viant/agently-core/runtime/streaming"
	agentsvc "github.com/viant/agently-core/service/agent"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	aguistore "github.com/viant/agently-core/app/store/agui"
	iauth "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/protocol/agui"
	"github.com/viant/jsonrpc"
	"github.com/viant/mcp-protocol/schema"
	tasks "github.com/viant/mcp-tasks"
	taskclient "github.com/viant/mcp-tasks/client"
	taskrpc "github.com/viant/mcp-tasks/rpc"
)

func mcpTasksOwnerContext() context.Context {
	return iauth.WithUserInfo(context.Background(), &iauth.UserInfo{Subject: "owner"})
}
func mcpTasksCaps() schema.ClientCapabilities {
	caps := schema.ClientCapabilities{Elicitation: &schema.ClientCapabilitiesElicitation{}}
	tasks.Advertise(&caps)
	return caps
}
func mcpTasksRun(t *testing.T, store aguistore.Store, ctx context.Context, thread, run, parent, mode string) *aguistore.Run {
	t.Helper()
	record, _, err := store.Admit(ctx, aguistore.Admission{Principal: "owner", ThreadID: thread, RunID: run, TurnID: "native-" + run, Input: rawAGUI(map[string]any{"threadId": thread, "runId": run, "messages": []any{}, "forwardedProps": map[string]any{"nativeMode": mode}})})
	require.NoError(t, err)
	record, err = store.Append(ctx, "owner", thread, run, record.Revision, []json.RawMessage{rawAGUI(map[string]any{"type": "RUN_STARTED", "threadId": thread, "runId": run})}, nil)
	require.NoError(t, err)
	return record
}
func mcpTasksFinish(t *testing.T, store aguistore.Store, ctx context.Context, record *aguistore.Run, result json.RawMessage) {
	t.Helper()
	latest, err := store.GetRun(ctx, record.Principal, record.ThreadID, record.RunID)
	require.NoError(t, err)
	_, err = store.Append(ctx, latest.Principal, latest.ThreadID, latest.RunID, latest.Revision, []json.RawMessage{rawAGUI(map[string]any{"type": "RUN_FINISHED", "threadId": latest.ThreadID, "runId": latest.RunID, "result": result, "outcome": map[string]any{"type": "success"}})}, nil)
	require.NoError(t, err)
}
func mcpTasksControls(store aguistore.Store, resume func(context.Context, tasks.Owner, MCPTaskReference, []agui.WireResumeEntry) error, cancel func(context.Context, tasks.Owner, MCPTaskReference) error) MCPTaskControls {
	return MCPTaskControls{Authorize: func(ctx context.Context, owner tasks.Owner, reference MCPTaskReference) error {
		run, err := store.GetRun(ctx, owner.Subject, reference.ThreadID, reference.RunID)
		if err != nil {
			return err
		}
		if run.Principal != owner.Subject {
			return tasks.ErrNotFound
		}
		return nil
	}, Snapshot: MCPTasksAGUISnapshot(store), Resume: resume, Cancel: cancel}
}

func TestMCPTasksNativeErrorCannotFabricateOriginalMCPResult(t *testing.T) {
	base, _ := newDurableAGUIServer(t)
	ctx := mcpTasksOwnerContext()
	native := mcpTasksRun(t, base.store, ctx, "native-error", "native", "", "detach")
	_, err := base.store.Append(ctx, "owner", native.ThreadID, native.RunID, native.Revision, []json.RawMessage{rawAGUI(map[string]any{"type": "RUN_ERROR", "message": "native execution error", "code": "native_error"})}, nil)
	require.NoError(t, err)
	_, err = MCPTasksAGUISnapshot(base.store)(ctx, tasks.Owner{Tenant: "workspace", Subject: "owner"}, MCPTaskReference{ThreadID: native.ThreadID, RunID: native.RunID})
	require.ErrorContains(t, err, "canonical MCP task error/result is unavailable")
	require.Zero(t, base.queries.Load())
}
func TestMCPTasksOwnedNativeAliasDurabilityAndDetachedScope(t *testing.T) {
	base, _ := newDurableAGUIServer(t)
	store := base.store
	ctx := mcpTasksOwnerContext()
	owner := tasks.Owner{Tenant: "workspace", Subject: "owner"}
	parent := mcpTasksRun(t, store, ctx, "parent-thread", "parent", "", "foreground")
	child := mcpTasksRun(t, store, ctx, "child-thread", "child", parent.RunID, "detach")
	var cancellations atomic.Int32
	controls := mcpTasksControls(store, func(context.Context, tasks.Owner, MCPTaskReference, []agui.WireResumeEntry) error { return nil }, func(_ context.Context, _ tasks.Owner, reference MCPTaskReference) error {
		require.Equal(t, child.RunID, reference.RunID)
		cancellations.Add(1)
		return nil
	})
	backend, err := NewMCPTasksBackend(store, controls, MCPTasksOptions{Tenant: "workspace", PollIntervalMS: 10})
	require.NoError(t, err)
	ref := MCPTaskReference{ThreadID: child.ThreadID, RunID: child.RunID, ParentRunID: parent.RunID, ExecutionMode: "detach", InvocationID: "registered-native-invocation"}
	created, err := backend.Create(ctx, owner, mcpTasksCaps(), MCPTaskOrigin(ref))
	require.NoError(t, err)
	require.Equal(t, tasks.Working, created.Status)
	alias, aliasErr := store.GetRun(ctx, owner.Subject, created.TaskID, created.TaskID)
	require.NoError(t, aliasErr)
	require.Equal(t, aguistore.StatusRunning, alias.Status)
	require.Empty(t, alias.TurnID, "alias must never become a recoverable native execution")
	require.True(t, isMCPTaskLedgerRun(alias))
	require.NoError(t, recoverAGUIDurable(ctx, base, base, store, alias))
	require.Zero(t, base.queries.Load(), "alias recovery must not run an agent")
	requestBody := rawAGUI(map[string]any{"threadId": created.TaskID, "runId": created.TaskID, "messages": []any{}})
	request, requestErr := http.NewRequestWithContext(ctx, http.MethodPost, "/v1/ag-ui/run", strings.NewReader(string(requestBody)))
	require.NoError(t, requestErr)
	recorder := httptest.NewRecorder()
	handleAGUIRun(base, nil)(recorder, request)
	require.Equal(t, http.StatusConflict, recorder.Code)
	require.Zero(t, base.queries.Load())
	mcpTasksFinish(t, store, ctx, parent, json.RawMessage(`{"resultType":"complete","content":[]}`))
	// Reconstructing the adapter reads the same alias and native child. No run,
	// executor or worker is created by polling/recovery after parent completion.
	restored, err := NewMCPTasksBackend(store, controls, MCPTasksOptions{Tenant: "workspace"})
	require.NoError(t, err)
	state, err := restored.Get(ctx, owner, created.TaskID)
	require.NoError(t, err)
	require.Equal(t, tasks.Working, state.Status)
	foreignCtx := iauth.WithUserInfo(context.Background(), &iauth.UserInfo{Subject: "foreign"})
	_, err = restored.Get(foreignCtx, tasks.Owner{Tenant: "workspace", Subject: "foreign"}, created.TaskID)
	require.ErrorIs(t, err, tasks.ErrNotFound)
	_, err = restored.Get(ctx, tasks.Owner{Tenant: "other-workspace", Subject: "owner"}, created.TaskID)
	require.ErrorIs(t, err, tasks.ErrNotFound)
	ack, err := restored.Cancel(ctx, owner, created.TaskID)
	require.NoError(t, err)
	require.Equal(t, "complete", ack.ResultType)
	_, err = restored.Cancel(ctx, owner, created.TaskID)
	require.NoError(t, err)
	require.EqualValues(t, 1, cancellations.Load())
	state, err = restored.Get(ctx, owner, created.TaskID)
	require.NoError(t, err)
	require.Equal(t, tasks.Working, state.Status, "cancel ack is not a terminal outcome")
	final := json.RawMessage(`{"resultType":"complete","content":[{"type":"text","text":"child result"}],"isError":true,"_meta":{"fixture":"host-only"}}`)
	mcpTasksFinish(t, store, ctx, child, final)
	state, err = restored.Get(ctx, owner, created.TaskID)
	require.NoError(t, err)
	require.Equal(t, tasks.Completed, state.Status)
	require.JSONEq(t, string(final), string(state.Result))
	require.Nil(t, state.Error)
}
func TestMCPTasksPartialInputsResumeOneOriginalRunAndFollowContinuation(t *testing.T) {
	base, _ := newDurableAGUIServer(t)
	store := base.store
	ctx := mcpTasksOwnerContext()
	owner := tasks.Owner{Tenant: "workspace", Subject: "owner"}
	original := mcpTasksRun(t, store, ctx, "input-thread", "initial", "", "fork")
	schemaObject := map[string]json.RawMessage{"type": rawAGUI("object"), "properties": json.RawMessage(`{"choice":{"type":"string"}}`), "required": json.RawMessage(`["choice"]`)}
	interrupts := []agui.WireInterrupt{{ID: "native-input-1", Reason: "elicitation", ResponseSchema: &schemaObject}, {ID: "native-input-2", Reason: "approval", ResponseSchema: &schemaObject}}
	pending := rawAGUI(aguiPending{Interrupts: interrupts})
	original, err := store.Append(ctx, "owner", original.ThreadID, original.RunID, original.Revision, []json.RawMessage{rawAGUI(map[string]any{"type": "RUN_FINISHED", "threadId": original.ThreadID, "runId": original.RunID, "outcome": map[string]any{"type": "interrupt", "interrupts": interrupts}})}, &aguistore.Change{Pending: pending})
	require.NoError(t, err)
	var resumes atomic.Int32
	controls := mcpTasksControls(store, func(_ context.Context, _ tasks.Owner, reference MCPTaskReference, answers []agui.WireResumeEntry) error {
		require.Equal(t, original.RunID, reference.RunID)
		require.Len(t, answers, 2)
		resumes.Add(1)
		current, e := store.GetRun(ctx, "owner", reference.ThreadID, reference.RunID)
		require.NoError(t, e)
		next, _, e := store.Admit(ctx, aguistore.Admission{Principal: "owner", ThreadID: reference.ThreadID, RunID: "continuation", ParentRunID: reference.RunID, TurnID: current.TurnID, PriorRunID: reference.RunID, ExpectedPriorRevision: current.Revision, Input: rawAGUI(map[string]any{"threadId": reference.ThreadID, "runId": "continuation", "parentRunId": reference.RunID, "messages": []any{}, "resume": answers})})
		require.NoError(t, e)
		next, e = store.Append(ctx, "owner", next.ThreadID, next.RunID, next.Revision, []json.RawMessage{rawAGUI(map[string]any{"type": "RUN_STARTED", "threadId": next.ThreadID, "runId": next.RunID})}, nil)
		require.NoError(t, e)
		mcpTasksFinish(t, store, ctx, next, json.RawMessage(`{"resultType":"complete","content":[{"type":"text","text":"resumed result"}]}`))
		return nil
	}, func(context.Context, tasks.Owner, MCPTaskReference) error { return nil })
	backend, err := NewMCPTasksBackend(store, controls, MCPTasksOptions{Tenant: "workspace"})
	require.NoError(t, err)
	created, err := backend.Create(ctx, owner, mcpTasksCaps(), MCPTaskOrigin(MCPTaskReference{ThreadID: original.ThreadID, RunID: original.RunID, ExecutionMode: "fork"}))
	require.NoError(t, err)
	require.Equal(t, tasks.InputRequired, created.Status)
	require.Len(t, created.InputRequests, 2)
	keys := []string{}
	for key := range created.InputRequests {
		keys = append(keys, key)
	}
	answer := json.RawMessage(`{"action":"accept","content":{"choice":"fixture"}}`)
	_, err = backend.Update(ctx, owner, created.TaskID, tasks.InputResponses{keys[0]: answer, "never-issued": answer})
	require.NoError(t, err)
	require.Zero(t, resumes.Load())
	state, err := backend.Get(ctx, owner, created.TaskID)
	require.NoError(t, err)
	require.Len(t, state.InputRequests, 1)
	_, err = backend.Update(ctx, owner, created.TaskID, tasks.InputResponses{keys[1]: answer})
	require.NoError(t, err)
	require.EqualValues(t, 1, resumes.Load())
	_, err = backend.Update(ctx, owner, created.TaskID, tasks.InputResponses{keys[0]: answer, keys[1]: answer})
	require.NoError(t, err)
	require.EqualValues(t, 1, resumes.Load())
	state, err = backend.Get(ctx, owner, created.TaskID)
	require.NoError(t, err)
	require.Equal(t, tasks.Completed, state.Status)
	require.Contains(t, string(state.Result), "resumed result")
}
func TestMCPTasksWireUsesExistingNativeJournalWithoutRunningTools(t *testing.T) {
	base, _ := newDurableAGUIServer(t)
	store := base.store
	ctx := mcpTasksOwnerContext()
	owner := tasks.Owner{Tenant: "workspace", Subject: "owner"}
	native := mcpTasksRun(t, store, ctx, "wire-native", "native", "", "wait")
	controls := mcpTasksControls(store, func(context.Context, tasks.Owner, MCPTaskReference, []agui.WireResumeEntry) error { return nil }, func(context.Context, tasks.Owner, MCPTaskReference) error { return nil })
	backend, err := NewMCPTasksBackend(store, controls, MCPTasksOptions{Tenant: "workspace"})
	require.NoError(t, err)
	created, err := backend.Create(ctx, owner, mcpTasksCaps(), MCPTaskOrigin(MCPTaskReference{ThreadID: native.ThreadID, RunID: native.RunID}))
	require.NoError(t, err)
	handler := &taskrpc.Handler{Service: backend, Owner: func(context.Context, *jsonrpc.Request) (tasks.Owner, error) { return owner, nil }}
	caller := taskrpc.CallerFunc(func(ctx context.Context, request taskrpc.Request) (json.RawMessage, error) {
		response := &jsonrpc.Response{}
		handler.Serve(ctx, &jsonrpc.Request{Jsonrpc: "2.0", Id: 1, Method: request.Method, Params: request.Params}, response)
		if response.Error != nil {
			return nil, &tasks.RPCError{Code: response.Error.Code, Message: response.Error.Message, Data: response.Error.Data}
		}
		return response.Result, nil
	})
	client, err := taskclient.New(caller, taskclient.Options{Tasks: true, Info: schema.Implementation{Name: "fixture", Version: "1"}, Capabilities: mcpTasksCaps()})
	require.NoError(t, err)
	state, err := client.Get(ctx, created.TaskID)
	require.NoError(t, err)
	require.Equal(t, tasks.Working, state.Status)
	mcpTasksFinish(t, store, ctx, native, json.RawMessage(`{"resultType":"complete","content":[{"type":"text","text":"normal native result"}]}`))
	result, err := client.Wait(ctx, created.TaskID, taskclient.WaitOptions{})
	require.NoError(t, err)
	require.Contains(t, string(result), "normal native result")
	require.Zero(t, base.queries.Load(), "task polling must not execute an agent query")
}
func TestMCPTasksUnknownResumeNeverReplaysAndTTL(t *testing.T) {
	base, _ := newDurableAGUIServer(t)
	store := base.store
	ctx := mcpTasksOwnerContext()
	owner := tasks.Owner{Tenant: "workspace", Subject: "owner"}
	original := mcpTasksRun(t, store, ctx, "unknown-input", "initial", "", "wait")
	schemaObject := map[string]json.RawMessage{"type": rawAGUI("object"), "properties": json.RawMessage(`{}`)}
	interrupt := agui.WireInterrupt{ID: "input", Reason: "elicitation", ResponseSchema: &schemaObject}
	original, err := store.Append(ctx, "owner", original.ThreadID, original.RunID, original.Revision, []json.RawMessage{rawAGUI(map[string]any{"type": "RUN_FINISHED", "threadId": original.ThreadID, "runId": original.RunID, "outcome": map[string]any{"type": "interrupt", "interrupts": []agui.WireInterrupt{interrupt}}})}, &aguistore.Change{Pending: rawAGUI(aguiPending{Interrupts: []agui.WireInterrupt{interrupt}})})
	require.NoError(t, err)
	var called atomic.Int32
	now := time.Now()
	ttl := int64(1000)
	controls := mcpTasksControls(store, func(context.Context, tasks.Owner, MCPTaskReference, []agui.WireResumeEntry) error {
		called.Add(1)
		return errors.New("fixture lost native acknowledgement")
	}, func(context.Context, tasks.Owner, MCPTaskReference) error { return nil })
	backend, err := NewMCPTasksBackend(store, controls, MCPTasksOptions{Tenant: "workspace", TTLMS: &ttl, Clock: func() time.Time { return now }})
	require.NoError(t, err)
	created, err := backend.Create(ctx, owner, mcpTasksCaps(), MCPTaskOrigin(MCPTaskReference{ThreadID: original.ThreadID, RunID: original.RunID}))
	require.NoError(t, err)
	key := ""
	for k := range created.InputRequests {
		key = k
	}
	response := tasks.InputResponses{key: json.RawMessage(`{"action":"accept","content":{}}`)}
	_, err = backend.Update(ctx, owner, created.TaskID, response)
	require.Error(t, err)
	_, err = backend.Update(ctx, owner, created.TaskID, response)
	require.Error(t, err)
	require.EqualValues(t, 1, called.Load())
	now = now.Add(time.Second)
	_, err = backend.Get(ctx, owner, created.TaskID)
	require.ErrorIs(t, err, tasks.ErrNotFound)
}

func TestMCPTasksConcreteAGUIResumeWakesOriginalLiveExecution(t *testing.T) {
	base, _ := newDurableAGUIServer(t)
	native := &liveInterruptAGUIClient{durableAGUIClient: base, wake: make(chan struct{})}
	native.query = func(ctx context.Context, input *agentsvc.QueryInput) (*agentsvc.QueryOutput, error) {
		if err := native.bus.Publish(ctx, &streaming.Event{Type: streaming.EventTypeElicitationRequested, ConversationID: input.ConversationID, TurnID: input.MessageID, ElicitationID: "live-elicitation"}); err != nil {
			return nil, err
		}
		<-native.wake
		if err := native.bus.Publish(ctx, &streaming.Event{Type: streaming.EventTypeTurnCompleted, ConversationID: input.ConversationID, TurnID: input.MessageID}); err != nil {
			return nil, err
		}
		return &agentsvc.QueryOutput{Content: "same native execution"}, nil
	}
	h := handleAGUIRun(native, nil)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h(w, r.WithContext(iauth.WithUserInfo(r.Context(), &iauth.UserInfo{Subject: "owner"})))
	}))
	defer server.Close()
	status, wire := durablePost(t, server, aguiChatRequest, nil)
	require.Equal(t, 200, status, wire)
	run, err := base.store.GetRun(mcpTasksOwnerContext(), "owner", "thread", "external-run")
	require.NoError(t, err)
	controls := MCPTasksAGUIControls(native, base.store, func(context.Context, tasks.Owner, MCPTaskReference) error { return nil })
	payload := rawAGUI(map[string]any{"choice": "yes"})
	err = controls.Resume(mcpTasksOwnerContext(), tasks.Owner{Tenant: "workspace", Subject: "owner"}, MCPTaskReference{ThreadID: run.ThreadID, RunID: run.RunID}, []agui.WireResumeEntry{{InterruptId: "live-elicitation", Status: "resolved", Payload: &payload}})
	require.NoError(t, err)
	require.Eventually(t, func() bool { return native.awake.Load() }, time.Second, time.Millisecond)
	require.Eventually(t, func() bool {
		original, err := base.store.GetRun(mcpTasksOwnerContext(), "owner", run.ThreadID, run.RunID)
		if err != nil || original.ResumedByRunID == "" {
			return false
		}
		continuation, err := base.store.GetRun(mcpTasksOwnerContext(), "owner", run.ThreadID, original.ResumedByRunID)
		return err == nil && continuation.Status == aguistore.StatusFinished
	}, time.Second, time.Millisecond)
	require.EqualValues(t, 1, native.queries.Load())
	require.Zero(t, native.resumes.Load(), "existing live execution must not be restarted")
}
