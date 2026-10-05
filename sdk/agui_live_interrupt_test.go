package sdk

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	aguistore "github.com/viant/agently-core/app/store/agui"
	iauth "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/protocol/agui"
	"github.com/viant/agently-core/runtime/aguistate"
	"github.com/viant/agently-core/runtime/streaming"
	agentsvc "github.com/viant/agently-core/service/agent"
)

type liveInterruptAGUIClient struct {
	*durableAGUIClient
	wake  chan struct{}
	awake atomic.Bool
}

func (c *liveInterruptAGUIClient) aguiInspectRun(ctx context.Context, record *aguistore.Run) (*aguiRecoveredNative, error) {
	if c.awake.Load() {
		return &aguiRecoveredNative{Status: "completed", Messages: []aguistate.Object{{"id": "live-answer", "role": "assistant", "content": "same native execution resumed"}}}, nil
	}
	schema := map[string]json.RawMessage{"type": rawAGUI("object"), "properties": json.RawMessage(`{"choice":{"type":"string"}}`), "required": json.RawMessage(`["choice"]`), "additionalProperties": rawAGUI(false)}
	return &aguiRecoveredNative{Status: "running", Pending: aguiPending{Interrupts: []agui.WireInterrupt{{ID: "live-elicitation", Reason: "elicitation", ResponseSchema: &schema}}}}, nil
}
func (c *liveInterruptAGUIClient) aguiApplyInterrupt(ctx context.Context, record *aguistore.Run, interrupt agui.WireInterrupt, answer agui.WireResumeEntry) (aguiInterruptDisposition, error) {
	if c.awake.CompareAndSwap(false, true) {
		close(c.wake)
	}
	return aguiInterruptWokeExisting, nil
}
func TestAGUIDurableLiveInterruptWakesOriginalExecution(t *testing.T) {
	base, _ := newDurableAGUIServer(t)
	client := &liveInterruptAGUIClient{durableAGUIClient: base, wake: make(chan struct{})}
	client.query = func(ctx context.Context, input *agentsvc.QueryInput) (*agentsvc.QueryOutput, error) {
		if err := client.bus.Publish(ctx, &streaming.Event{Type: streaming.EventTypeElicitationRequested, ConversationID: input.ConversationID, TurnID: input.MessageID, ElicitationID: "live-elicitation"}); err != nil {
			return nil, err
		}
		<-client.wake
		if err := client.bus.Publish(ctx, &streaming.Event{Type: streaming.EventTypeTextDelta, ConversationID: input.ConversationID, TurnID: input.MessageID, AssistantMessageID: "live-answer", Content: "same native execution resumed"}); err != nil {
			return nil, err
		}
		if err := client.bus.Publish(ctx, &streaming.Event{Type: streaming.EventTypeTurnCompleted, ConversationID: input.ConversationID, TurnID: input.MessageID}); err != nil {
			return nil, err
		}
		return &agentsvc.QueryOutput{Content: "same native execution resumed"}, nil
	}
	h := handleAGUIRun(client, nil)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h(w, r.WithContext(iauth.WithUserInfo(r.Context(), &iauth.UserInfo{Subject: "owner"})))
	}))
	defer server.Close()
	status, wire := durablePost(t, server, aguiChatRequest, nil)
	require.Equal(t, 200, status, wire)
	require.Contains(t, wire, `"type":"interrupt"`)
	require.Contains(t, wire, `"id":"live-elicitation"`)
	require.False(t, client.awake.Load(), "handoff must preserve the blocking native execution")
	body := `{"threadId":"thread","runId":"live-resume","messages":[],"resume":[{"interruptId":"live-elicitation","status":"resolved","payload":{"choice":"yes"}}]}`
	status, wire = durablePost(t, server, body, nil)
	require.Equal(t, 200, status, wire)
	require.Contains(t, wire, "same native execution resumed")
	assertAGUITerminal(t, decodeAGUISSE(t, strings.NewReader(wire)), "RUN_FINISHED")
	require.EqualValues(t, 1, client.queries.Load())
	require.EqualValues(t, 0, client.resumes.Load(), "a live waiter must not restart the turn")
}
