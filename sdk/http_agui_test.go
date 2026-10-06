package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/protocol/agui"
	agentsvc "github.com/viant/agently-core/service/agent"
)

func TestHTTPAGUIRunRetainsAuthLargeEventsAndOpaqueCursor(t *testing.T) {
	large := strings.Repeat("界", 1<<20)
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		require.Equal(t, "/v1/ag-ui/run", r.URL.Path)
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "Bearer fixture-token", r.Header.Get("Authorization"))
		require.Equal(t, "true", r.Header.Get("X-Agently-Debug"))
		var input agui.RunAgentInput
		require.NoError(t, json.NewDecoder(r.Body).Decode(&input))
		require.Equal(t, " wire ", input.ThreadID)
		require.Equal(t, " run ", input.RunID)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "id: cursor:one\r\ndata: {\"type\":\"RUN_STARTED\",\"threadId\":\" wire \",\"runId\":\" run \"}\r\n\r\n")
		body, _ := json.Marshal(map[string]any{"type": "TEXT_MESSAGE_CONTENT", "messageId": "m", "delta": large})
		fmt.Fprintf(w, "id: cursor:two\ndata: %s\n\n", body)
		fmt.Fprint(w, "id: cursor:three\ndata: {\"type\":\"RUN_FINISHED\",\n data: invalid\n")
	}))
	defer server.Close()
	client, err := NewHTTP(server.URL, WithAuthToken("fixture-token"), WithSessionDebug("debug"))
	require.NoError(t, err)
	stream, err := client.RunAGUI(context.Background(), &agui.RunAgentInput{ThreadID: " wire ", RunID: " run ", Messages: []agui.Message{}}, nil)
	require.NoError(t, err)
	defer stream.Close()
	event, err := stream.Recv()
	require.NoError(t, err)
	require.Equal(t, "RUN_STARTED", event.Type)
	event, err = stream.Recv()
	require.NoError(t, err)
	var content struct{ Delta string }
	require.NoError(t, event.Decode(&content))
	require.Equal(t, large, content.Delta)
	require.Equal(t, "cursor:two", stream.LastEventID())
	_, err = stream.Recv()
	var interrupted *AGUIObservationError
	require.ErrorAs(t, err, &interrupted)
	require.Equal(t, " run ", interrupted.RunID)
	require.Equal(t, "cursor:two", interrupted.LastEventID)
	require.Equal(t, 1, requests, "uncertain observation must not redispatch")
}
func TestHTTPAGUIAttachUsesExistingIdentityAndMultilineTerminal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "opaque-last", r.Header.Get("Last-Event-ID"))
		var input agui.RunAgentInput
		require.NoError(t, json.NewDecoder(r.Body).Decode(&input))
		require.Empty(t, input.Messages)
		require.Equal(t, "saved-run", input.RunID)
		var forwarded struct{ Agently struct{ Operation string } }
		require.NoError(t, json.Unmarshal(input.ForwardedProps, &forwarded))
		require.Equal(t, "run.attach", forwarded.Agently.Operation)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, ": heartbeat\n\nid: next\ndata: {\"type\":\"RUN_FINISHED\",\ndata: \"threadId\":\"thread\",\"runId\":\"saved-run\",\"outcome\":{\"type\":\"success\"}}\n\n")
	}))
	defer server.Close()
	client, err := NewHTTP(server.URL)
	require.NoError(t, err)
	stream, err := client.AttachAGUI(context.Background(), "thread", "saved-run", "opaque-last")
	require.NoError(t, err)
	defer stream.Close()
	event, err := stream.Recv()
	require.NoError(t, err)
	require.Equal(t, "RUN_FINISHED", event.Type)
	_, err = stream.Recv()
	require.True(t, errors.Is(err, io.EOF))
}
func TestHTTPClientHeadlessScopesIncludeBaseAndSelectedSurface(t *testing.T) {
	require.Equal(t, []string{"openid", "base", "cli"}, defaultCLIAuthScopes(&OAuthConfigResponse{Scopes: []string{"openid", "base"}, CLIScopes: []string{"cli"}, WebUIScopes: []string{"web"}}))
	require.Equal(t, []string{"openid", "web"}, defaultCLIAuthScopes(&OAuthConfigResponse{Scopes: []string{"openid"}, WebUIScopes: []string{"web"}}))
	require.Equal(t, []string{"openid", "mobile"}, defaultCLIAuthScopes(&OAuthConfigResponse{Scopes: []string{"openid"}, MobileUIScopes: []string{"mobile"}}))
}

func TestPrepareAGUIChatPreservesScopeAndRejectsNativeOnlyControls(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		require.Equal(t, "/v1/conversations/native", r.URL.Path)
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "native", "aguiThreadId": " wire "})
	}))
	defer server.Close()
	client, err := NewHTTP(server.URL)
	require.NoError(t, err)
	input := &agentsvc.QueryInput{ConversationID: "native", MessageID: "source-id", AgentID: "agent", Query: "task", ModelOverride: "model", ToolsAllowed: []string{"allowed"}, Context: map[string]interface{}{"exact": json.Number("9007199254740993")}}
	prepared, err := client.PrepareAGUIChat(context.Background(), input)
	require.NoError(t, err)
	require.Equal(t, "native", prepared.ConversationID)
	require.Equal(t, " wire ", prepared.Input.ThreadID)
	require.Equal(t, "source-id", prepared.Input.Messages[0].ID)
	require.Contains(t, string(prepared.Input.ForwardedProps), `9007199254740993`)
	require.Contains(t, string(prepared.Input.ForwardedProps), `"useServerState":true`)
	require.Equal(t, "native", input.ConversationID, "preparation must not rewrite caller's native identity")
	before := requests
	input.UserId = "forged-owner"
	_, err = client.PrepareAGUIChat(context.Background(), input)
	require.ErrorContains(t, err, "UserId")
	require.Equal(t, before, requests, "unsupported authority rejected before I/O")
}
func TestAGUICollectorDoesNotReturnHistoricalOrChildText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range []map[string]any{
			{"type": "RUN_STARTED", "threadId": "thread", "runId": "run"},
			{"type": "MESSAGES_SNAPSHOT", "messages": []any{map[string]any{"id": "old", "role": "assistant", "content": "historical"}}},
			{"type": "TEXT_MESSAGE_CONTENT", "messageId": "child", "subagentRunId": "child-run", "delta": "child only"},
			{"type": "TEXT_MESSAGE_CONTENT", "messageId": "current", "delta": "current"},
			{"type": "RUN_FINISHED", "threadId": "thread", "runId": "run", "outcome": map[string]string{"type": "success"}},
		} {
			body, _ := json.Marshal(event)
			fmt.Fprintf(w, "data: %s\n\n", body)
		}
	}))
	defer server.Close()
	client, err := NewHTTP(server.URL)
	require.NoError(t, err)
	stream, err := client.RunAGUI(context.Background(), &agui.RunAgentInput{ThreadID: "thread", RunID: "run", Messages: []agui.Message{}}, nil)
	require.NoError(t, err)
	defer stream.Close()
	result, err := CollectAGUI(stream, "native", nil)
	require.NoError(t, err)
	require.Equal(t, "current", result.Content)
	require.Equal(t, "native", result.ConversationID)
}
func TestOldConversationWireRoutesAreNotMounted(t *testing.T) {
	handler := NewHandler(nil)
	for _, path := range []string{"/v1/agent/query", "/v1/stream?conversationId=thread"} {
		method := http.MethodGet
		if strings.Contains(path, "query") {
			method = http.MethodPost
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
		require.Equal(t, http.StatusNotFound, rec.Code)
	}
}
