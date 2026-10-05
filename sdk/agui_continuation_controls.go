package sdk

import (
	"context"
	"encoding/json"
	"fmt"

	aguistore "github.com/viant/agently-core/app/store/agui"
	"github.com/viant/agently-core/protocol/agui"
	agentsvc "github.com/viant/agently-core/service/agent"
)

// Continuation clients cannot replace backend execution controls. Restore them
// from the immutable original admission, including across multiple interrupts.
func restoreAGUIContinuationControls(ctx context.Context, store aguistore.Store, record *aguistore.Run, query *agentsvc.QueryInput) error {
	if record == nil || query == nil || store == nil {
		return fmt.Errorf("continuation control scope is required")
	}
	if record.PriorRunID == "" {
		return nil
	}
	if query.ConversationID != record.ConversationID || query.MessageID != record.TurnID || query.UserId != record.Principal {
		return fmt.Errorf("continuation query identity mismatch")
	}
	ancestor := record
	seen := map[string]bool{record.RunID: true}
	for depth := 0; ancestor.PriorRunID != ""; depth++ {
		if depth >= 64 || seen[ancestor.PriorRunID] {
			return fmt.Errorf("continuation lineage is cyclic or exceeds its bound")
		}
		expected := ancestor.PriorRunID
		loaded, err := store.GetRun(ctx, record.Principal, record.ThreadID, expected)
		if err != nil {
			return err
		}
		if loaded == nil || loaded.RunID != expected || loaded.Principal != record.Principal || loaded.ThreadID != record.ThreadID || loaded.ConversationID != record.ConversationID || loaded.TurnID != record.TurnID {
			return fmt.Errorf("continuation ancestor identity mismatch")
		}
		seen[expected] = true
		ancestor = loaded
	}
	var input agui.RunAgentInput
	if err := json.Unmarshal(ancestor.Input, &input); err != nil {
		return err
	}
	if input.ThreadID != record.ThreadID || input.RunID != ancestor.RunID {
		return fmt.Errorf("original accepted input identity mismatch")
	}
	var properties agui.ForwardedProps
	if len(input.ForwardedProps) > 0 {
		if err := json.Unmarshal(input.ForwardedProps, &properties); err != nil {
			return err
		}
	}
	if properties.Agently == nil {
		return nil
	}
	if properties.Agently.Operation != "chat" {
		return fmt.Errorf("continuation ancestor is not a chat execution")
	}
	agent, model := query.AgentID, query.ModelOverride
	var reserved any
	present := false
	if query.Context != nil {
		reserved, present = query.Context["agui"]
	}
	if err := applyAGUIExecution(query, properties.Agently, false); err != nil {
		return err
	}
	if agent != "" {
		query.AgentID = agent
	}
	if model != "" {
		query.ModelOverride = model
	}
	if present {
		query.Context["agui"] = reserved
	}
	return nil
}
