package sdk

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	aguistore "github.com/viant/agently-core/app/store/agui"
	"github.com/viant/agently-core/protocol/agui"
	"github.com/viant/agently-core/runtime/aguistate"
	"github.com/viant/agently-core/runtime/clienttool"
	agentsvc "github.com/viant/agently-core/service/agent"
)

func TestAGUIDurableWaitingOutputRecoversOriginalNativeClientCalls(t *testing.T) {
	c := newRecoveryAGUIClient(t)
	c.query = func(context.Context, *agentsvc.QueryInput) (*agentsvc.QueryOutput, error) {
		return &agentsvc.QueryOutput{ExecutionStatus: "waiting_for_user"}, nil
	}
	inspections := 0
	var originalCall clienttool.PendingCall
	c.inspect = func(_ context.Context, run *aguistore.Run) (*aguiRecoveredNative, error) {
		inspections++
		originalCall = clienttool.PendingCall{ID: "original-call", ProtocolID: agui.ProtocolToolCallID(run.TurnID, "original-call"), Name: "browser", Arguments: map[string]interface{}{}, ConversationID: run.ConversationID, TurnID: run.TurnID, ToolMessageID: "native-tool", AssistantMessageID: "native-assistant"}
		return &aguiRecoveredNative{Status: "waiting_for_user", Pending: aguiPending{ClientTools: []clienttool.PendingCall{originalCall}}, Messages: []aguistate.Object{{"id": originalCall.AssistantMessageID, "role": "assistant", "toolCalls": []any{map[string]any{"id": originalCall.ProtocolID, "type": "function", "function": map[string]any{"name": originalCall.Name, "arguments": "{}"}}}}}}, nil
	}
	h := handleAGUIRun(c, nil)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { h(w, r.WithContext(recoveryContext())) }))
	defer server.Close()
	body := `{"threadId":"thread","runId":"boundary-run","messages":[{"id":"user-message","role":"user","content":"fixture"}],"tools":[{"name":"browser","description":"fixture","parameters":{"type":"object"}}]}`
	status, wire := durablePost(t, server, body, nil)
	require.Equal(t, http.StatusOK, status, wire)
	events := decodeAGUISSE(t, strings.NewReader(wire))
	assertAGUITerminal(t, events, "RUN_FINISHED")
	require.Contains(t, wire, `"pendingToolCallIds":["`+originalCall.ProtocolID+`"]`)
	require.Contains(t, wire, `"toolCallName":"browser"`)
	require.Equal(t, 1, inspections)
	record, err := c.store.GetRun(recoveryContext(), "owner", "thread", "boundary-run")
	require.NoError(t, err)
	var pending aguiPending
	require.NoError(t, json.Unmarshal(record.Pending, &pending))
	require.Equal(t, []clienttool.PendingCall{originalCall}, pending.ClientTools)
	status, replay := durablePost(t, server, body, nil)
	require.Equal(t, http.StatusOK, status, replay)
	require.Equal(t, wire, replay)
	require.EqualValues(t, 1, c.queries.Load())
	require.Equal(t, 1, inspections)
	require.Zero(t, c.resumes.Load())
	require.Zero(t, c.completions.Load())
}
