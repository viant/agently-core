package sdk

import (
	"context"
	"encoding/json"
	"fmt"

	aguistore "github.com/viant/agently-core/app/store/agui"
	"github.com/viant/agently-core/runtime/clienttool"
	"github.com/viant/agently-core/runtime/requestctx"
)

// An external input cannot create an empty-message native turn: ordinary runs
// have a server-generated turn and a client-message ID; resource runs have no
// native turn. This internal origin plus the principal-scoped causal parent is
// the durable proof used to resume a detached linked conversation.
func aguiDetachedResumeContext(ctx context.Context, store aguistore.Store, record *aguistore.Run) (context.Context, error) {
	if record == nil || store == nil {
		return nil, fmt.Errorf("continuation origin scope is required")
	}
	original := record
	seen := map[string]bool{record.RunID: true}
	for original.PriorRunID != "" {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		expected := original.PriorRunID
		if seen[expected] {
			return nil, fmt.Errorf("continuation origin lineage is cyclic")
		}
		loaded, err := store.GetRun(ctx, record.Principal, record.ThreadID, expected)
		if err != nil {
			return nil, err
		}
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		if loaded == nil || loaded.RunID != expected || loaded.Principal != record.Principal || loaded.ThreadID != record.ThreadID || loaded.ConversationID != record.ConversationID || loaded.TurnID != record.TurnID {
			return nil, fmt.Errorf("continuation origin ancestor identity mismatch")
		}
		seen[expected] = true
		original = loaded
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if original.ParentRunID == "" || original.TurnID == "" || original.ClientMessageID != "" {
		return ctx, nil
	}
	var input struct {
		Messages       []json.RawMessage `json:"messages"`
		ForwardedProps struct {
			Invocation *requestctx.Invocation `json:"agentlyInvocation"`
		} `json:"forwardedProps"`
	}
	if err := json.Unmarshal(original.Input, &input); err != nil {
		return nil, err
	}
	inv := input.ForwardedProps.Invocation
	if inv == nil {
		return ctx, nil
	}
	if len(input.Messages) != 0 || !inv.Detached || inv.ExecutionMode != "detach" || inv.ID != original.RunID || inv.ConversationID != original.ConversationID || inv.TurnID != original.TurnID || inv.ParentConversationID == "" || inv.ParentTurnID == "" {
		return nil, fmt.Errorf("invalid detached protocol origin")
	}
	parentThread, err := aguiWireThreadForConversation(ctx, store, record.Principal, inv.ParentConversationID)
	if err != nil {
		return nil, err
	}
	parent, err := store.GetRun(ctx, record.Principal, parentThread, original.ParentRunID)
	if err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if parent == nil || parent.RunID != original.ParentRunID || parent.Principal != record.Principal || parent.ThreadID != parentThread || parent.TurnID != inv.ParentTurnID || parent.ConversationID != inv.ParentConversationID {
		return nil, fmt.Errorf("detached parent protocol scope mismatch")
	}
	return clienttool.WithDetachedContinuation(ctx, clienttool.DetachedContinuation{ChildConversationID: original.ConversationID, ChildTurnID: original.TurnID, ParentConversationID: parent.ConversationID, ParentTurnID: parent.TurnID, ParentProtocolRunID: parent.RunID, ExecutionMode: "detach"}), nil
}
