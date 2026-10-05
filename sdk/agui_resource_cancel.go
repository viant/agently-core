package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	aguistore "github.com/viant/agently-core/app/store/agui"
)

// Observer subscriptions have no native turn to cancel. Their durable terminal
// transition fences future appends and renewals in every process. It does not
// report cancellation of filesystem or other effects already in flight.
func cancelAGUIResourceObserver(ctx context.Context, store aguistore.Store, record *aguistore.Run) (bool, bool, error) {
	if record.TurnID != "" {
		return false, false, nil
	}
	var input struct {
		ForwardedProps struct {
			Agently struct {
				Operation string `json:"operation"`
			} `json:"agently"`
		} `json:"forwardedProps"`
	}
	if err := json.Unmarshal(record.Input, &input); err != nil {
		return true, false, err
	}
	switch input.ForwardedProps.Agently.Operation {
	case "goal.subscribe", "feed.subscribe":
	default:
		return true, false, nil
	}
	for attempt := 0; attempt < 8; attempt++ {
		if aguiRunTerminal(record.Status) {
			return true, false, nil
		}
		if record.Status == aguistore.StatusInterrupted {
			return true, false, fmt.Errorf("resource subscription cannot own an interrupted model turn")
		}
		events := []json.RawMessage{}
		if record.LastSequence == 0 {
			events = append(events, rawAGUI(map[string]any{"type": "RUN_STARTED", "threadId": record.ThreadID, "runId": record.RunID, "protocolVersion": "1.0"}))
		}
		events = append(events, rawAGUI(map[string]any{"type": "RUN_FINISHED", "threadId": record.ThreadID, "runId": record.RunID, "outcome": map[string]any{"type": "cancelled"}}))
		_, err := store.Append(ctx, record.Principal, record.ThreadID, record.RunID, record.Revision, events, &aguistore.Change{LeaseOwner: record.LeaseOwner})
		if err == nil {
			return true, true, nil
		}
		if !errors.Is(err, aguistore.ErrConflict) && !errors.Is(err, aguistore.ErrInvalidTransition) {
			return true, false, err
		}
		record, err = store.GetRun(ctx, record.Principal, record.ThreadID, record.RunID)
		if err != nil {
			return true, false, err
		}
		// An expired lease cannot authorize the controller's append. Claim it as
		// the same owner solely for the cancellation transition; no work is rerun.
		if !aguiRunTerminal(record.Status) && record.LeaseUntil != nil && !record.LeaseUntil.After(time.Now()) {
			previous := record
			record, err = store.Claim(ctx, previous.Principal, previous.ThreadID, previous.RunID, previous.Revision, previous.LeaseOwner, time.Minute)
			if err != nil && !errors.Is(err, aguistore.ErrConflict) {
				return true, false, err
			}
			if err != nil {
				record, err = store.GetRun(ctx, previous.Principal, previous.ThreadID, previous.RunID)
				if err != nil {
					return true, false, err
				}
			}
		}
	}
	return true, false, aguistore.ErrConflict
}
