package sdk

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
