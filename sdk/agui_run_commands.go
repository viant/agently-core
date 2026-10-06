package sdk

import (
	"context"
	"encoding/json"
	"fmt"

	aguistore "github.com/viant/agently-core/app/store/agui"
	"github.com/viant/agently-core/protocol/agui/extensions"
)

func dispatchAGUIRunCommand(ctx context.Context, client Client, store aguistore.Store, record *aguistore.Run, operation string, payload json.RawMessage) (any, error) {
	if err := extensions.ValidateRunPayload(operation, payload); err != nil {
		return nil, err
	}
	input := struct {
		RunID string `json:"runId"`
		After int64  `json:"after"`
		Limit int    `json:"limit"`
	}{Limit: 256}
	if err := json.Unmarshal(payload, &input); err != nil {
		return nil, err
	}
	if input.RunID == record.RunID {
		return nil, fmt.Errorf("run control target must differ from its command run")
	}
	target, err := store.GetRun(ctx, record.Principal, record.ThreadID, input.RunID)
	if err != nil {
		return nil, err
	}
	switch operation {
	case "run.get":
		result := map[string]any{"version": "1", "threadId": target.ThreadID, "runId": target.RunID, "parentRunId": target.ParentRunID, "status": target.Status, "revision": target.Revision, "lastSequence": target.LastSequence}
		if target.ResumedByRunID != "" {
			result["resumedByRunId"] = target.ResumedByRunID
		}
		return result, nil
	case "run.cancel":
		handled, cancelled, err := cancelAGUIResourceObserver(ctx, store, target)
		if !handled {
			cancelled, err = cancelAGUIRun(ctx, client, store, record.Principal, record.ThreadID, input.RunID)
		}
		return map[string]any{"version": "1", "runId": input.RunID, "cancelled": cancelled}, err
	case "run.events.list":
		if input.After > target.LastSequence {
			return nil, fmt.Errorf("event cursor exceeds target run journal")
		}
		entries, err := store.Replay(ctx, record.Principal, record.ThreadID, input.RunID, input.After, input.Limit)
		if err != nil {
			return nil, err
		}
		events := make([]map[string]any, 0, len(entries))
		after := input.After
		for _, entry := range entries {
			events = append(events, map[string]any{"cursor": aguiCursor(record.ThreadID, input.RunID, entry.Sequence), "sequence": entry.Sequence, "event": json.RawMessage(entry.Event)})
			after = entry.Sequence
		}
		return map[string]any{"version": "1", "runId": input.RunID, "events": events, "hasMore": after < target.LastSequence}, nil
	}
	return nil, fmt.Errorf("unsupported run operation %q", operation)
}
