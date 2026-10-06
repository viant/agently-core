package sdk

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	aguistore "github.com/viant/agently-core/app/store/agui"
	"github.com/viant/agently-core/protocol/agui"
	"github.com/viant/agently-core/runtime/mcpapps"
	mcpschema "github.com/viant/mcp-protocol/schema"
)

func TestMCPAppsLongNativeJournalDispatchRenewsLease(t *testing.T) {
	if os.Getenv("AGENTLY_TEST_LONG_MCP_APPS") != "1" {
		t.Skip("65-second real HTTP fixture; enable AGENTLY_TEST_LONG_MCP_APPS=1")
	}
	client, _ := newDurableAGUIServer(t)
	ctx := context.Background()
	var effects atomic.Int32
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Method string `json:"method"`
		}
		if json.NewDecoder(r.Body).Decode(&input) != nil || input.Method != "tools/call" {
			http.Error(w, "unexpected request", 400)
			return
		}
		effects.Add(1)
		time.Sleep(65 * time.Second)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"resultType":"complete","content":[{"type":"text","text":"safe"}],"structuredContent":{"slow":true},"_meta":{"receipt":"private"}}}`)
	}))
	defer fixture.Close()
	input := &agui.RunAgentInput{ThreadID: "slow-proxy", RunID: "slow-proxy"}
	record, _, err := client.store.Admit(ctx, aguistore.Admission{Principal: "owner", ThreadID: input.ThreadID, RunID: input.RunID, TurnID: "slow-native-turn", Input: rawAGUI(map[string]any{"threadId": input.ThreadID, "runId": input.RunID, "messages": []any{}})})
	require.NoError(t, err)
	binding := AGUIMCPAppsBindings{App: AGUIMCPAppBinding{AppInstanceID: "issued", ThreadID: "original", ServerID: "native", ServerHash: "hash", ResourceURI: "ui://slow"}, Authorize: func(context.Context, AGUIMCPAppBinding) error { return nil }, AuthorizeTool: func(context.Context, AGUIMCPAppBinding, string) error { return nil }, ToolCaller: func(ctx context.Context, input *MCPUIToolCallInput) (*MCPUIToolCallOutput, error) {
		if input.ConversationID != "original" {
			return nil, fmt.Errorf("wrong original conversation")
		}
		request, e := http.NewRequestWithContext(ctx, http.MethodPost, fixture.URL, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"slow"}}`))
		if e != nil {
			return nil, e
		}
		response, e := fixture.Client().Do(request)
		if e != nil {
			return nil, e
		}
		defer response.Body.Close()
		var envelope struct {
			Result mcpschema.CallToolResult `json:"result"`
		}
		if e = json.NewDecoder(response.Body).Decode(&envelope); e != nil {
			return nil, e
		}
		return &MCPUIToolCallOutput{Status: "completed"}, mcpapps.Record(ctx, "native", "slow", "slow-native-op", &envelope.Result)
	}}
	workerCtx := WithAGUIMCPAppsBindings(ctx, binding)
	proxy := &MCPAppsProxyRequest{ServerID: "native", ServerHash: "hash", Method: "tools/call", Params: map[string]json.RawMessage{"name": json.RawMessage(`"slow"`)}}
	done := make(chan error, 1)
	go func() { done <- runAGUIMCPProxyWorker(workerCtx, client, client.store, record, input, proxy, nil) }()
	require.Eventually(t, func() bool { return effects.Load() == 1 }, 5*time.Second, 10*time.Millisecond)
	started, err := client.store.GetRun(ctx, "owner", input.ThreadID, input.RunID)
	require.NoError(t, err)
	require.NotNil(t, started.LeaseUntil)
	initialLease := *started.LeaseUntil
	time.Sleep(35 * time.Second)
	renewed, err := client.store.GetRun(ctx, "owner", input.ThreadID, input.RunID)
	require.NoError(t, err)
	require.Equal(t, aguistore.StatusRunning, renewed.Status)
	require.NotNil(t, renewed.LeaseUntil)
	require.True(t, renewed.LeaseUntil.After(initialLease), "native observer lease must extend during slow remote effect")
	require.ErrorIs(t, runAGUIMCPProxyWorker(workerCtx, client, client.store, renewed, input, proxy, nil), aguistore.ErrConflict)
	require.Equal(t, int32(1), effects.Load())
	select {
	case err = <-done:
		require.NoError(t, err)
	case <-time.After(40 * time.Second):
		t.Fatal("slow proxy worker did not finish")
	}
	finished, err := client.store.GetRun(ctx, "owner", input.ThreadID, input.RunID)
	require.NoError(t, err)
	require.Equal(t, aguistore.StatusFinished, finished.Status)
	replay, err := client.store.Replay(ctx, "owner", input.ThreadID, input.RunID, 0, 10)
	require.NoError(t, err)
	require.Len(t, replay, 2)
	require.Contains(t, string(replay[1].Event), `"receipt":"private"`)
	require.ErrorIs(t, runAGUIMCPProxyWorker(workerCtx, client, client.store, finished, input, proxy, nil), aguistore.ErrInvalidTransition)
	require.Equal(t, int32(1), effects.Load())
	thread, err := client.store.GetThread(ctx, "owner", input.ThreadID)
	require.NoError(t, err)
	require.NotContains(t, string(thread.State), "private")
	require.NotContains(t, string(thread.Messages), "private")
}
