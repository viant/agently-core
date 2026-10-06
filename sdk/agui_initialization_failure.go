package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	aguistore "github.com/viant/agently-core/app/store/agui"
)

// Initialization never dispatches native execution. Record a durable error under
// the still-owned live lease without rewriting shared messages/state. A lost
// lease or another worker's progress remains observation/recovery responsibility.
func journalAGUIInitializationFailure(ctx context.Context, client Client, store aguistore.Store, claimed *aguistore.Run, cause error) error {
	if claimed == nil || claimed.LeaseOwner == "" || cause == nil || errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	latest, err := store.GetRun(ctx, claimed.Principal, claimed.ThreadID, claimed.RunID)
	if err != nil {
		return err
	}
	if aguiRunTerminal(latest.Status) || latest.LeaseOwner != claimed.LeaseOwner || latest.LeaseUntil == nil || !latest.LeaseUntil.After(time.Now()) {
		return nil
	}
	var events []json.RawMessage
	if latest.LastSequence == 0 {
		events = append(events, rawAGUI(map[string]any{"type": "RUN_STARTED", "threadId": latest.ThreadID, "runId": latest.RunID}))
	}
	events = append(events, rawAGUI(map[string]any{"type": "RUN_ERROR", "message": "Could not initialize the run", "code": "INITIALIZATION_FAILED"}))
	if _, err = store.Append(ctx, latest.Principal, latest.ThreadID, latest.RunID, latest.Revision, events, &aguistore.Change{LeaseOwner: claimed.LeaseOwner}); err != nil {
		return err
	}
	notifyAGUIRunUpdated(ctx, client, latest.ThreadID)
	return nil
}
