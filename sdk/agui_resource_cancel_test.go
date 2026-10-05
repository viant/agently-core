package sdk

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	aguistore "github.com/viant/agently-core/app/store/agui"
)

func TestAGUIResourceCancellationFencesObserverJournalAndLease(t *testing.T) {
	for _, operation := range []string{"goal.subscribe", "feed.subscribe"} {
		t.Run(operation, func(t *testing.T) {
			client := newRecoveryAGUIClient(t)
			ctx := recoveryContext()
			input := rawAGUI(map[string]any{"threadId": "thread", "runId": "observer", "messages": []any{}, "forwardedProps": map[string]any{"agently": map[string]any{"version": "1", "operation": operation, "requestId": "observer", "payload": map[string]any{}}}})
			run, _, err := client.store.Admit(ctx, aguistore.Admission{Principal: "owner", ThreadID: "thread", RunID: "observer", Input: input})
			require.NoError(t, err)
			run, err = client.store.Claim(ctx, "owner", "thread", "observer", run.Revision, "live-observer", time.Minute)
			require.NoError(t, err)
			run, err = client.store.Append(ctx, "owner", "thread", "observer", run.Revision, []json.RawMessage{rawAGUI(map[string]any{"type": "RUN_STARTED", "threadId": "thread", "runId": "observer"})}, &aguistore.Change{LeaseOwner: run.LeaseOwner})
			require.NoError(t, err)
			command := &aguistore.Run{Principal: "owner", ThreadID: "thread", RunID: "cancel-command"}
			result, err := dispatchAGUIRunCommand(ctx, client, client.store, command, "run.cancel", json.RawMessage(`{"runId":"observer"}`))
			require.NoError(t, err)
			require.Equal(t, true, result.(map[string]any)["cancelled"])
			current, err := client.store.GetRun(ctx, "owner", "thread", "observer")
			require.NoError(t, err)
			require.Equal(t, aguistore.StatusCancelled, current.Status)
			_, err = client.store.Renew(ctx, "owner", "thread", "observer", run.LeaseRevision, run.LeaseOwner, time.Minute)
			require.Error(t, err)
			_, err = client.store.Append(ctx, "owner", "thread", "observer", run.Revision, []json.RawMessage{rawAGUI(map[string]any{"type": "CUSTOM", "name": "late", "value": 1})}, &aguistore.Change{LeaseOwner: run.LeaseOwner})
			require.Error(t, err)
			events, err := client.store.Replay(ctx, "owner", "thread", "observer", 0, 10)
			require.NoError(t, err)
			require.Len(t, events, 2)
			require.Contains(t, string(events[1].Event), `"type":"cancelled"`)
			result, err = dispatchAGUIRunCommand(ctx, client, client.store, command, "run.cancel", json.RawMessage(`{"runId":"observer"}`))
			require.NoError(t, err)
			require.Equal(t, false, result.(map[string]any)["cancelled"])
		})
	}
}
