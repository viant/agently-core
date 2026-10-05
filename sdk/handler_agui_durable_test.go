package sdk

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	aguistore "github.com/viant/agently-core/app/store/agui"
	conversation "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/app/store/native"
	iauth "github.com/viant/agently-core/internal/auth"
	convservice "github.com/viant/agently-core/internal/service/conversation"
	"github.com/viant/agently-core/protocol/agui"
	"github.com/viant/agently-core/runtime/clienttool"
	"github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/runtime/streaming"
	agentsvc "github.com/viant/agently-core/service/agent"
	workspace "github.com/viant/agently-core/service/workspace"
)

type durableAGUIClient struct {
	*aguiTestClient
	store            aguistore.Store
	resumes          atomic.Int32
	completions      atomic.Int32
	completedContent string
}

func (c *durableAGUIClient) aguiStore() aguistore.Store { return c.store }
func (c *durableAGUIClient) aguiResume(ctx context.Context, q *agentsvc.QueryInput, turnID string, out *agentsvc.QueryOutput) error {
	c.resumes.Add(1)
	out.ConversationID = q.ConversationID
	out.TurnID = turnID
	out.Content = "continued answer"
	return nil
}
func (c *durableAGUIClient) aguiCompleteTool(_ context.Context, _ clienttool.PendingCall, raw json.RawMessage, _ string) error {
	c.completions.Add(1)
	return json.Unmarshal(raw, &c.completedContent)
}
func newDurableAGUIServer(t *testing.T) (*durableAGUIClient, *httptest.Server) {
	t.Helper()
	ctx := context.Background()
	backend, err := native.New(ctx, native.Options{WorkspaceRoot: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(func() { _ = backend.Shutdown(ctx) })
	nativeConversations, err := convservice.New(ctx, backend)
	require.NoError(t, err)
	owned := conversation.NewConversation()
	owned.SetId("thread")
	owned.SetCreatedByUserID("owner")
	require.NoError(t, nativeConversations.PatchConversations(ctx, owned))
	c := &durableAGUIClient{aguiTestClient: newAGUITestClient(), store: aguistore.New(backend)}
	c.owner = "owner"
	h := handleAGUIRun(c, nil)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h(w, r.WithContext(iauth.WithUserInfo(r.Context(), &iauth.UserInfo{Subject: "owner"})))
	}))
	t.Cleanup(server.Close)
	return c, server
}
func durablePost(t *testing.T, server *httptest.Server, body string, headers map[string]string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, server.URL, strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	client := server.Client()
	client.Timeout = 10 * time.Second
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	wire, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, string(wire)
}

func TestAGUIDurableReplayAndInputIdentity(t *testing.T) {
	c, server := newDurableAGUIServer(t)
	c.query = func(ctx context.Context, in *agentsvc.QueryInput) (*agentsvc.QueryOutput, error) {
		_ = c.bus.Publish(ctx, &streaming.Event{Type: streaming.EventTypeTextDelta, ConversationID: in.ConversationID, TurnID: in.MessageID, MessageID: "assistant", Content: "hello"})
		return &agentsvc.QueryOutput{Content: "hello"}, nil
	}
	status, wire := durablePost(t, server, aguiChatRequest, nil)
	require.Equal(t, 200, status, wire)
	events := decodeAGUISSE(t, strings.NewReader(wire))
	assertAGUITerminal(t, events, "RUN_FINISHED")
	status, replay := durablePost(t, server, aguiChatRequest, nil)
	require.Equal(t, 200, status, replay)
	require.Equal(t, wire, replay)
	require.EqualValues(t, 1, c.queries.Load())
	status, replay = durablePost(t, server, aguiChatRequest, map[string]string{"Last-Event-ID": aguiCursor("thread", "external-run", 2)})
	require.Equal(t, 200, status, replay)
	require.NotContains(t, replay, `"type":"RUN_STARTED"`)
	tampered := strings.Replace(aguiChatRequest, `"hello"`, `"different"`, 1)
	status, _ = durablePost(t, server, tampered, nil)
	require.Equal(t, 409, status)
	status, _ = durablePost(t, server, strings.Replace(aguiChatRequest, `external-run`, `other-run`, 1), nil)
	require.Equal(t, 409, status)
	status, _ = durablePost(t, server, aguiChatRequest, map[string]string{"Last-Event-ID": aguiCursor("foreign", "external-run", 1)})
	require.Equal(t, 400, status)
}

func TestAGUIDurableClientToolContinuationAndReplay(t *testing.T) {
	c, server := newDurableAGUIServer(t)
	c.query = func(ctx context.Context, in *agentsvc.QueryInput) (*agentsvc.QueryOutput, error) {
		return &agentsvc.QueryOutput{ClientToolCalls: []clienttool.PendingCall{{ID: "call", Name: "browser", Arguments: map[string]any{"n": 1}, ConversationID: in.ConversationID, TurnID: in.MessageID, AssistantMessageID: "assistant", ToolMessageID: "native-tool"}}}, nil
	}
	status, wire := durablePost(t, server, aguiChatRequest, nil)
	require.Equal(t, 200, status, wire)
	accepted, err := c.store.GetRun(context.Background(), "owner", "thread", "external-run")
	require.NoError(t, err)
	alias := agui.ProtocolToolCallID(accepted.TurnID, "call")
	require.Contains(t, wire, `"pendingToolCallIds":["`+alias+`"]`)
	require.Contains(t, wire, `"toolCallId":"`+alias+`"`)
	require.Contains(t, wire, "TOOL_CALL_ARGS")
	body := `{"threadId":"thread","runId":"continued","messages":[{"id":"client-tool-result","role":"tool","toolCallId":"call","content":"browser result"}]}`
	status, _ = durablePost(t, server, body, nil)
	require.Equal(t, 409, status, "native provider ID must not resolve a public alias")
	body = strings.Replace(body, `"toolCallId":"call"`, `"toolCallId":"`+alias+`"`, 1)
	status, wire = durablePost(t, server, body, nil)
	require.Equal(t, 200, status, wire)
	require.Contains(t, wire, "continued answer")
	assertAGUITerminal(t, decodeAGUISSE(t, strings.NewReader(wire)), "RUN_FINISHED")
	require.EqualValues(t, 1, c.queries.Load())
	require.EqualValues(t, 1, c.resumes.Load())
	require.EqualValues(t, 1, c.completions.Load())
	require.Equal(t, "browser result", c.completedContent)
	status, replay := durablePost(t, server, body, nil)
	require.Equal(t, 200, status, replay)
	require.Equal(t, wire, replay)
	require.EqualValues(t, 1, c.resumes.Load())
	require.EqualValues(t, 1, c.completions.Load())
	thread, err := c.store.GetThread(context.Background(), "owner", "thread")
	require.NoError(t, err)
	require.Contains(t, string(thread.Messages), "client-tool-result")
	require.Contains(t, string(thread.Messages), "continued answer")
}

func TestAGUIDurableRecoversAdmissionBeforeExecution(t *testing.T) {
	c, server := newDurableAGUIServer(t)
	c.query = func(ctx context.Context, in *agentsvc.QueryInput) (*agentsvc.QueryOutput, error) {
		return &agentsvc.QueryOutput{Content: "recovered admission"}, nil
	}
	_, fresh, err := c.store.Admit(context.Background(), aguistore.Admission{ThreadID: "thread", RunID: "external-run", Principal: "owner", TurnID: "user-message", Input: json.RawMessage(aguiChatRequest)})
	require.NoError(t, err)
	require.True(t, fresh)
	status, wire := durablePost(t, server, aguiChatRequest, nil)
	require.Equal(t, 200, status, wire)
	require.Contains(t, wire, "recovered admission")
	require.EqualValues(t, 1, c.queries.Load())
}

func TestAGUIDurableSharedStateCommandPersistsIntoModelContext(t *testing.T) {
	c, server := newDurableAGUIServer(t)
	command := `{"threadId":"thread","runId":"state-command","messages":[],"forwardedProps":{"agently":{"version":"1","operation":"state.patch","requestId":"patch-1","payload":{"patch":[{"op":"replace","path":"","value":{"counter":9007199254740993}}]}}}}`
	status, wire := durablePost(t, server, command, nil)
	require.Equal(t, 200, status, wire)
	require.Contains(t, wire, `"type":"STATE_DELTA"`)
	require.Contains(t, wire, "9007199254740993")
	assertAGUITerminal(t, decodeAGUISSE(t, strings.NewReader(wire)), "RUN_FINISHED")
	c.query = func(ctx context.Context, input *agentsvc.QueryInput) (*agentsvc.QueryOutput, error) {
		protocolContext := input.Context["agui"].(map[string]any)
		require.JSONEq(t, `{"counter":9007199254740993}`, string(protocolContext["state"].(json.RawMessage)))
		return &agentsvc.QueryOutput{Content: "state available"}, nil
	}
	status, wire = durablePost(t, server, aguiChatRequest, nil)
	require.Equal(t, 200, status, wire)
	require.Contains(t, wire, "9007199254740993")
	require.Contains(t, wire, "state available")
	status, replay := durablePost(t, server, command, nil)
	require.Equal(t, 200, status, replay)
	require.Contains(t, replay, "9007199254740993")
	require.EqualValues(t, 1, c.queries.Load())
}

func TestAGUIDurableCausalChildEventsBeforeRootFinish(t *testing.T) {
	c, server := newDurableAGUIServer(t)
	c.query = func(ctx context.Context, in *agentsvc.QueryInput) (*agentsvc.QueryOutput, error) {
		invocation := requestctx.Invocation{ID: "child-run", ConversationID: "child-thread", TurnID: "child-turn", Name: "research", ParentConversationID: in.ConversationID, ParentTurnID: in.MessageID, ParentToolCallID: "delegate"}
		childCtx, err := requestctx.ObserveInvocation(ctx, invocation)
		if err != nil {
			return nil, err
		}
		if err = c.bus.Publish(childCtx, &streaming.Event{Type: streaming.EventTypeTextDelta, ConversationID: invocation.ConversationID, TurnID: invocation.TurnID, AssistantMessageID: "child-answer", Content: "child response"}); err != nil {
			return nil, err
		}
		if err = requestctx.NotifyInvocationReturned(childCtx, requestctx.InvocationResult{Invocation: invocation, NativeStatus: "succeeded", Content: "child response"}); err != nil {
			return nil, err
		}
		return &agentsvc.QueryOutput{Content: "root response"}, nil
	}
	status, wire := durablePost(t, server, aguiChatRequest, nil)
	require.Equal(t, 200, status, wire)
	require.Contains(t, wire, `"type":"SUBAGENT_STARTED"`)
	require.Contains(t, wire, `"subagentRunId":"child-run"`)
	require.Contains(t, wire, "child response")
	require.Contains(t, wire, `"type":"SUBAGENT_FINISHED"`)
	require.Less(t, strings.Index(wire, `"type":"SUBAGENT_FINISHED"`), strings.LastIndex(wire, `"type":"RUN_FINISHED"`))
	status, replay := durablePost(t, server, aguiChatRequest, nil)
	require.Equal(t, 200, status, replay)
	require.Equal(t, wire, replay)
	require.EqualValues(t, 1, c.queries.Load())
}

func TestAGUIDurableResourceCommandsDoNotEnterChat(t *testing.T) {
	c, original := newDurableAGUIServer(t)
	c.query = func(ctx context.Context, in *agentsvc.QueryInput) (*agentsvc.QueryOutput, error) {
		return &agentsvc.QueryOutput{Content: "saved answer"}, nil
	}
	status, _ := durablePost(t, original, aguiChatRequest, nil)
	require.Equal(t, 200, status)
	metadata := workspace.NewMetadataHandler(nil, nil, "test-metadata")
	h := handleAGUIRun(c, nil, AGUIWorkspaceBindings{Metadata: metadata})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h(w, r.WithContext(iauth.WithUserInfo(r.Context(), &iauth.UserInfo{Subject: "owner"})))
	}))
	defer server.Close()
	execute := func(operation, runID string, payload any) string {
		body, err := json.Marshal(map[string]any{"threadId": "thread", "runId": runID, "messages": []any{}, "forwardedProps": map[string]any{"agently": map[string]any{"version": "1", "operation": operation, "requestId": runID, "payload": payload}}})
		require.NoError(t, err)
		status, wire := durablePost(t, server, string(body), nil)
		require.Equal(t, 200, status, wire)
		assertAGUITerminal(t, decodeAGUISSE(t, strings.NewReader(wire)), "RUN_FINISHED")
		status, replay := durablePost(t, server, string(body), nil)
		require.Equal(t, 200, status, replay)
		require.Equal(t, wire, replay)
		return wire
	}
	threadBefore, err := c.store.GetThread(context.Background(), "owner", "thread")
	require.NoError(t, err)
	require.Contains(t, execute("workspace.metadata.get", "metadata", map[string]any{}), "test-metadata")
	require.Contains(t, execute("run.get", "inspect", map[string]any{"runId": "external-run"}), `"status":"finished"`)
	require.Contains(t, execute("run.events.list", "events", map[string]any{"runId": "external-run", "limit": 1}), `"hasMore":true`)
	threadAfter, err := c.store.GetThread(context.Background(), "owner", "thread")
	require.NoError(t, err)
	require.JSONEq(t, string(threadBefore.State), string(threadAfter.State))
	require.JSONEq(t, string(threadBefore.Messages), string(threadAfter.Messages))
	require.EqualValues(t, 1, c.queries.Load())
}

func TestAGUIDurablePresetForgeContentUsesActivity(t *testing.T) {
	c, server := newDurableAGUIServer(t)
	c.query = func(ctx context.Context, in *agentsvc.QueryInput) (*agentsvc.QueryOutput, error) {
		return &agentsvc.QueryOutput{Content: "Report:\n```forge-data\n{\"id\":\"rows\",\"data\":[{\"private_authoring\":1}]}\n```\nDone."}, nil
	}
	status, wire := durablePost(t, server, aguiChatRequest, nil)
	require.Equal(t, 200, status, wire)
	require.Contains(t, wire, "ACTIVITY_SNAPSHOT")
	for _, event := range decodeAGUISSE(t, strings.NewReader(wire)) {
		if strings.HasPrefix(event.Type, "TEXT_MESSAGE") {
			require.NotContains(t, event.Delta, "private_authoring")
			require.NotContains(t, event.Delta, "forge-data")
		}
	}
	require.Contains(t, wire, aguiInteractiveFallback)
}

func TestAGUIDurableNestedHandoffCarriesLeafInterrupt(t *testing.T) {
	c, server := newDurableAGUIServer(t)
	c.query = func(ctx context.Context, in *agentsvc.QueryInput) (*agentsvc.QueryOutput, error) {
		leaf := clienttool.PendingCall{ID: "call", Name: "browser", Arguments: map[string]any{"n": 1}, ConversationID: "child-thread", TurnID: "child-turn", AssistantMessageID: "leaf-assistant", ToolMessageID: "leaf-tool"}
		inv := requestctx.Invocation{ID: "child-run", ConversationID: leaf.ConversationID, TurnID: leaf.TurnID, Name: "research", ParentConversationID: in.ConversationID, ParentTurnID: in.MessageID, ParentToolCallID: "delegate"}
		childCtx, err := requestctx.ObserveInvocation(ctx, inv)
		if err != nil {
			return nil, err
		}
		if err = requestctx.NotifyInvocationReturned(childCtx, requestctx.InvocationResult{Invocation: inv, NativeStatus: "waiting_for_user", ClientToolCalls: []clienttool.PendingCall{leaf}}); err != nil {
			return nil, err
		}
		edge := clienttool.Dependency{ID: "edge", ParentCall: clienttool.PendingCall{ID: "delegate", ConversationID: in.ConversationID, TurnID: in.MessageID, ToolMessageID: "parent-op"}, ChildConversationID: leaf.ConversationID, ChildTurnID: leaf.TurnID, ResultAdapter: clienttool.AgentRunResultV1, Waiting: true}
		return &agentsvc.QueryOutput{ClientToolCalls: []clienttool.PendingCall{leaf}, ClientToolDependencies: []clienttool.Dependency{edge}}, nil
	}
	status, wire := durablePost(t, server, aguiChatRequest, nil)
	require.Equal(t, 200, status, wire)
	require.Contains(t, wire, `"reason":"agently.client_tool"`)
	require.Contains(t, wire, `"kind":"client-tool"`)
	alias := agui.ProtocolToolCallID("child-turn", "call")
	require.Contains(t, wire, `"toolCallId":"`+alias+`"`)
	require.NotContains(t, wire, "pendingToolCallIds")
	events := decodeAGUISSE(t, strings.NewReader(wire))
	calls := 0
	for _, event := range events {
		if event.Type == "TOOL_CALL_START" && event.ToolCallID == alias {
			calls++
			require.Contains(t, string(event.Standard), `"subagentRunId":"child-run"`)
		}
	}
	require.Equal(t, 1, calls, "leaf call must be streamed exactly once under its real child")
}

func TestAGUIDurableDetachedChildOutlivesRoot(t *testing.T) {
	c, server := newDurableAGUIServer(t)
	release := make(chan struct{})
	completed := make(chan error, 1)
	c.query = func(ctx context.Context, in *agentsvc.QueryInput) (*agentsvc.QueryOutput, error) {
		inv := requestctx.Invocation{Detached: true, ExecutionMode: "detach", ID: "detached-run", ConversationID: "detached-thread", TurnID: "detached-turn", Name: "background", ParentConversationID: in.ConversationID, ParentTurnID: in.MessageID}
		go func() {
			<-release
			childCtx, err := requestctx.ObserveInvocation(ctx, inv)
			if err == nil {
				err = requestctx.NotifyInvocationReturned(childCtx, requestctx.InvocationResult{Invocation: inv, NativeStatus: "succeeded", Content: "late child answer"})
			}
			completed <- err
		}()
		return &agentsvc.QueryOutput{Content: "root finished"}, nil
	}
	status, wire := durablePost(t, server, aguiChatRequest, nil)
	require.Equal(t, 200, status, wire)
	require.NotContains(t, wire, "late child answer")
	require.NotContains(t, wire, "SUBAGENT_STARTED")
	close(release)
	select {
	case err := <-completed:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("detached child did not complete")
	}
	record, err := c.store.GetRun(context.Background(), "owner", "detached-thread", "detached-run")
	require.NoError(t, err)
	require.Equal(t, aguistore.StatusFinished, record.Status)
	require.Equal(t, "external-run", record.ParentRunID)
	status, childWire := durablePost(t, server, string(record.Input), nil)
	require.Equal(t, 200, status, childWire)
	require.Contains(t, childWire, `"parentRunId":"external-run"`)
	require.Contains(t, childWire, "late child answer")
	assertAGUITerminal(t, decodeAGUISSE(t, strings.NewReader(childWire)), "RUN_FINISHED")
	require.EqualValues(t, 1, c.queries.Load())
}

func TestAGUIDurableRejectsUnsupportedProtocolBeforeAdmission(t *testing.T) {
	client, server := newDurableAGUIServer(t)
	body := strings.Replace(aguiChatRequest, `"threadId":"thread"`, `"protocolVersion":"2.0","threadId":"thread"`, 1)
	status, wire := durablePost(t, server, body, nil)
	require.Equal(t, 400, status, wire)
	require.Contains(t, wire, "unsupported protocolVersion")
	require.EqualValues(t, 0, client.queries.Load())
	_, err := client.store.GetRun(context.Background(), "owner", "thread", "external-run")
	require.ErrorIs(t, err, aguistore.ErrNotFound)
}
