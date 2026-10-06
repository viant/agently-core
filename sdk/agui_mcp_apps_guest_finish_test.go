package sdk

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	aguistore "github.com/viant/agently-core/app/store/agui"
	"github.com/viant/agently-core/protocol/agui"
)

type guestFinishRecorder struct {
	*durableAGUIClient
	thread, turn, status string
}

func (c *guestFinishRecorder) aguiFinishMCPGuestTurn(_ context.Context, thread, turn, status string, _ error) error {
	c.thread, c.turn, c.status = thread, turn, status
	return nil
}
func TestMCPAppsRecoveredToolErrorDoesNotMarkNativeGuestSuccessful(t *testing.T) {
	base, _ := newDurableAGUIServer(t)
	client := &guestFinishRecorder{durableAGUIClient: base}
	ctx := WithAGUIMCPAppsBindings(context.Background(), AGUIMCPAppsBindings{App: AGUIMCPAppBinding{ThreadID: "native-thread"}})
	run, _, err := base.store.Admit(ctx, aguistore.Admission{Principal: "owner", ThreadID: "proxy-thread", RunID: "proxy-run", TurnID: "host-turn", Input: json.RawMessage(`{"threadId":"proxy-thread","runId":"proxy-run","messages":[]}`)})
	require.NoError(t, err)
	receipt := json.RawMessage(`{"content":[{"type":"text","text":"tool failed"}],"isError":true}`)
	run, err = base.store.Append(ctx, "owner", "proxy-thread", "proxy-run", run.Revision, []json.RawMessage{rawAGUI(map[string]any{"type": "RUN_STARTED", "threadId": "proxy-thread", "runId": "proxy-run"})}, &aguistore.Change{Pending: rawAGUI(aguiMCPProxyPending{HostResult: receipt, NativeTurnID: "host-turn"})})
	require.NoError(t, err)
	require.NoError(t, runAGUIMCPProxyWorker(ctx, client, base.store, run, &agui.RunAgentInput{ThreadID: "proxy-thread", RunID: "proxy-run"}, &MCPAppsProxyRequest{Method: "tools/call"}, nil))
	require.Equal(t, "native-thread", client.thread)
	require.Equal(t, "host-turn", client.turn)
	require.Equal(t, "failed", client.status)
	final, err := base.store.GetRun(ctx, "owner", "proxy-thread", "proxy-run")
	require.NoError(t, err)
	require.Equal(t, aguistore.StatusFinished, final.Status, "an MCP tool error is still a real completed RPC result")
}
