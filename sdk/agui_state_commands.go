package sdk

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	aguistore "github.com/viant/agently-core/app/store/agui"
	"github.com/viant/agently-core/protocol/agui/extensions"
	"github.com/viant/agently-core/runtime/aguistate"
)

type AGUIStateResult struct {
	Version string          `json:"version"`
	Hash    string          `json:"hash"`
	State   json.RawMessage `json:"state"`
}

// runAGUIStateCommand commits shared state and its complete successful event
// sequence atomically through the native protocol writer. Chat messages remain
// outside this capability. Error journaling belongs to the enclosing worker.
func runAGUIStateCommand(ctx context.Context, store aguistore.Store, record *aguistore.Run, operation string, payload json.RawMessage) error {
	if record == nil || store == nil || record.TurnID != "" {
		return fmt.Errorf("state command requires its own resource run")
	}
	if operation != "state.get" && operation != "state.patch" {
		return fmt.Errorf("unsupported state command %q", operation)
	}
	if err := extensions.ValidateStatePayload(operation, payload); err != nil {
		return err
	}
	if err := validateAcceptedGoalCommand(record, operation, payload); err != nil {
		return fmt.Errorf("state command differs from accepted request: %w", err)
	}
	input := struct {
		Patch   json.RawMessage `json:"patch"`
		IfMatch string          `json:"ifMatch"`
	}{}
	if len(bytes.TrimSpace(payload)) > 0 {
		if err := json.Unmarshal(payload, &input); err != nil {
			return err
		}
	}
	var baselineHash string
	for attempt := 0; attempt < 8; attempt++ {
		latest, err := store.GetRun(ctx, record.Principal, record.ThreadID, record.RunID)
		if err != nil {
			return err
		}
		if latest.Status == aguistore.StatusFinished {
			return nil
		}
		if latest.Revision != record.Revision || latest.LeaseOwner != record.LeaseOwner {
			return aguistore.ErrConflict
		}
		thread, err := store.GetThread(ctx, record.Principal, record.ThreadID)
		if err != nil {
			return err
		}
		hash, err := aguiStateHash(thread.State)
		if err != nil {
			return err
		}
		if baselineHash == "" {
			baselineHash = hash
		} else if baselineHash != hash {
			return aguistore.ErrConflict
		}
		if input.IfMatch != "" && input.IfMatch != hash {
			return aguistore.ErrConflict
		}
		nextState := append(json.RawMessage(nil), thread.State...)
		if operation == "state.patch" {
			nextState, err = aguistate.ApplyPatchJSON(thread.State, input.Patch)
			if err != nil {
				return err
			}
		}
		nextHash, err := aguiStateHash(nextState)
		if err != nil {
			return err
		}
		events := []json.RawMessage{}
		if record.LastSequence == 0 {
			events = append(events, rawAGUI(map[string]any{"type": "RUN_STARTED", "threadId": record.ThreadID, "runId": record.RunID, "protocolVersion": "1.0"}))
		}
		events = append(events, rawAGUI(map[string]any{"type": "STATE_SNAPSHOT", "snapshot": thread.State}))
		change := &aguistore.Change{LeaseOwner: record.LeaseOwner}
		if operation == "state.patch" {
			events = append(events, rawAGUI(map[string]any{"type": "STATE_DELTA", "delta": input.Patch}))
			change.State = nextState
			change.ExpectedThreadRevision = thread.Revision
		}
		events = append(events, rawAGUI(map[string]any{"type": "CUSTOM", "name": "agently.state.result", "value": AGUIStateResult{Version: "1", Hash: nextHash, State: nextState}}), rawAGUI(map[string]any{"type": "RUN_FINISHED", "threadId": record.ThreadID, "runId": record.RunID, "outcome": map[string]any{"type": "success"}}))
		_, err = store.Append(ctx, record.Principal, record.ThreadID, record.RunID, record.Revision, events, change)
		if err == nil {
			return nil
		}
		if !errors.Is(err, aguistore.ErrConflict) {
			return err
		}
		// Only a revision change with the exact same canonical state can retry.
		// Another successful state patch must not be rebased or overwritten.
	}
	return aguistore.ErrConflict
}
func aguiStateHash(raw []byte) (string, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return "", err
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(canonical)
	return "s1:" + hex.EncodeToString(digest[:]), nil
}
