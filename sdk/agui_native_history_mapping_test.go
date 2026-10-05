package sdk

import (
	"context"
	"encoding/json"
	"github.com/viant/agently-core/runtime/requestctx"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	aguistore "github.com/viant/agently-core/app/store/agui"
	conversation "github.com/viant/agently-core/app/store/conversation"
	iauth "github.com/viant/agently-core/internal/auth"
	agentsvc "github.com/viant/agently-core/service/agent"
)

type mappedHistoryClient struct{ *datlyObservedClient }

func (c *mappedHistoryClient) GetConversation(ctx context.Context, id string) (*conversation.Conversation, error) {
	return c.native.GetConversation(ctx, id)
}
func (c *mappedHistoryClient) GetLiveState(ctx context.Context, id string, opts ...TranscriptOption) (*ConversationStateResponse, error) {
	return c.native.GetLiveState(ctx, id, opts...)
}
func (c *mappedHistoryClient) GetTranscript(ctx context.Context, in *GetTranscriptInput, opts ...TranscriptOption) (*ConversationStateResponse, error) {
	return c.native.GetTranscript(ctx, in, opts...)
}
func (c *mappedHistoryClient) aguiThreadReference(ctx context.Context, id string) (*string, error) {
	return c.native.aguiThreadReference(ctx, id)
}
func (c *mappedHistoryClient) aguiConversationBootstrapAvailable() bool {
	return c.native.aguiConversationBootstrapAvailable()
}

func TestAGUIAuthenticatedOpaqueHistoryReentryUsesSameBindingAndJournal(t *testing.T) {
	c := &mappedHistoryClient{newDatlyObservedClient(t, 128)}
	wireThread := "  Opaque-雪\t"
	c.query = func(_ context.Context, in *agentsvc.QueryInput) (*agentsvc.QueryOutput, error) {
		return &agentsvc.QueryOutput{ConversationID: in.ConversationID, Content: "Saved answer"}, nil
	}
	aguiHandler := handleAGUIRun(c, nil)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal := r.Header.Get("Fixture-Principal")
		if principal == "" {
			principal = "owner"
		}
		r = r.WithContext(iauth.WithUserInfo(r.Context(), &iauth.UserInfo{Subject: principal}))
		if r.Method == http.MethodGet {
			r.SetPathValue("id", strings.TrimPrefix(r.URL.Path, "/native/"))
			handleGetConversation(c)(w, r)
			return
		}
		aguiHandler(w, r)
	}))
	defer server.Close()
	request := rawAGUI(map[string]any{"threadId": wireThread, "runId": "original", "messages": []any{map[string]any{"id": "user", "role": "user", "content": "Hello"}}})
	status, events := durablePost(t, server, string(request), nil)
	require.Equal(t, 200, status, events)
	assertAGUITerminal(t, decodeAGUISSE(t, strings.NewReader(events)), "RUN_FINISHED")
	record, err := c.store.GetRun(recoveryContext(), "owner", wireThread, "original")
	require.NoError(t, err)
	require.NotEqual(t, wireThread, record.ConversationID)
	response, err := server.Client().Get(server.URL + "/native/" + record.ConversationID)
	require.NoError(t, err)
	raw, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.Equal(t, 200, response.StatusCode, string(raw))
	var metadata map[string]any
	require.NoError(t, json.Unmarshal(raw, &metadata))
	require.Equal(t, wireThread, metadata["aguiThreadId"])
	body := rawAGUI(map[string]any{"threadId": metadata["aguiThreadId"], "runId": "history-read", "messages": []any{}, "forwardedProps": map[string]any{"agently": map[string]any{"version": "1", "operation": "conversation.bootstrap", "requestId": "history-read", "payload": map[string]any{"mode": "live"}}}})
	status, bootstrap := durablePost(t, server, string(body), nil)
	require.Equal(t, 200, status, bootstrap)
	assertAGUITerminal(t, decodeAGUISSE(t, strings.NewReader(bootstrap)), "RUN_FINISHED")
	require.Contains(t, bootstrap, `"conversationId":"`+record.ConversationID+`"`)
	after, err := c.store.GetRun(recoveryContext(), "owner", wireThread, "original")
	require.NoError(t, err)
	require.Equal(t, record.LastSequence, after.LastSequence)
	require.Equal(t, record.ConversationID, after.ConversationID)
	require.EqualValues(t, 1, c.queries.Load())
	resolver := c.store.(aguistore.ConversationThreadResolver)
	bound, err := resolver.GetThreadByConversationID(recoveryContext(), "owner", record.ConversationID)
	require.NoError(t, err)
	require.Equal(t, wireThread, bound.ThreadID)
	foreign, _ := http.NewRequest(http.MethodGet, server.URL+"/native/"+record.ConversationID, nil)
	foreign.Header.Set("Fixture-Principal", "foreign")
	rejected, err := server.Client().Do(foreign)
	require.NoError(t, err)
	foreignRaw, err := io.ReadAll(rejected.Body)
	require.NoError(t, err)
	require.NoError(t, rejected.Body.Close())
	require.NotContains(t, string(foreignRaw), "aguiThreadId")
}

func TestAGUIDetachedNativeChildWithOpaqueMappedParentKeepsLateAnswer(t *testing.T) {
	c := &mappedHistoryClient{newDatlyObservedClient(t, 128)}
	release := make(chan struct{})
	completed := make(chan error, 1)
	wireParent := "  Opaque-parent-雪\t"
	childID := "native-detached-child"
	child := conversation.NewConversation()
	child.SetId(childID)
	child.SetCreatedByUserID("owner")
	require.NoError(t, c.conv.PatchConversations(recoveryContext(), child))
	var nativeParent string
	c.query = func(ctx context.Context, in *agentsvc.QueryInput) (*agentsvc.QueryOutput, error) {
		nativeParent = in.ConversationID
		invocation := requestctx.Invocation{Detached: true, ExecutionMode: "detach", ID: "mapped-detached", ConversationID: childID, TurnID: "child-turn", Name: "background", ParentConversationID: in.ConversationID, ParentTurnID: in.MessageID}
		go func() {
			<-release
			childCtx, err := requestctx.ObserveInvocation(ctx, invocation)
			if err == nil {
				err = requestctx.NotifyInvocationReturned(childCtx, requestctx.InvocationResult{Invocation: invocation, NativeStatus: "succeeded", Content: "late mapped child answer"})
			}
			completed <- err
		}()
		return &agentsvc.QueryOutput{Content: "parent finished"}, nil
	}
	handler := handleAGUIRun(c, nil)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handler(w, r.WithContext(recoveryContext())) }))
	defer server.Close()
	body := rawAGUI(map[string]any{"threadId": wireParent, "runId": "mapped-parent", "messages": []any{map[string]any{"id": "user", "role": "user", "content": "Hello"}}})
	status, rootWire := durablePost(t, server, string(body), nil)
	require.Equal(t, 200, status, rootWire)
	require.NotEqual(t, wireParent, nativeParent)
	require.NotContains(t, rootWire, "late mapped child answer")
	close(release)
	select {
	case err := <-completed:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("detached child did not finish")
	}
	record, err := c.store.GetRun(recoveryContext(), "owner", childID, "mapped-detached")
	require.NoError(t, err)
	require.Equal(t, childID, record.ConversationID)
	require.Equal(t, "mapped-parent", record.ParentRunID)
	status, replay := durablePost(t, server, string(record.Input), nil)
	require.Equal(t, 200, status, replay)
	require.Contains(t, replay, "late mapped child answer")
	assertAGUITerminal(t, decodeAGUISSE(t, strings.NewReader(replay)), "RUN_FINISHED")
	require.EqualValues(t, 1, c.queries.Load())
}

func TestAGUIDetachedRejectsUnboundNativeInvocationWithoutObserving(t *testing.T) {
	c, _ := newDurableAGUIServer(t)
	record, _, err := c.store.Admit(recoveryContext(), aguistore.Admission{ThreadID: "thread", RunID: "parent", TurnID: "parent-turn", Principal: "owner", Input: rawAGUI(map[string]any{"threadId": "thread", "runId": "parent", "messages": []any{map[string]any{"id": "user-message", "role": "user", "content": "hello"}}})})
	require.NoError(t, err)
	observer := newAGUIInvocationObserver(recoveryContext(), c, record.ConversationID, record.TurnID)
	observer.store, observer.run, observer.runtime = c.store, record, c
	invocation := requestctx.Invocation{Detached: true, ExecutionMode: "detach", ID: "unbound-child-run", ConversationID: "missing-native-child", TurnID: "child-turn", Name: "child", ParentConversationID: record.ConversationID, ParentTurnID: record.TurnID}
	_, err = startAGUIDetached(recoveryContext(), observer, invocation)
	require.ErrorContains(t, err, "native binding mismatch")
	require.False(t, c.subscribed.Load())
	require.EqualValues(t, 0, c.queries.Load())
	child, err := c.store.GetRun(recoveryContext(), "owner", invocation.ConversationID, invocation.ID)
	require.NoError(t, err)
	require.EqualValues(t, 0, child.LastSequence)
	require.Nil(t, child.LeaseUntil)
}
