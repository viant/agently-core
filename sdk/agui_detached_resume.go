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
	original := record
	seen := map[string]bool{}
	for depth := 0; original.PriorRunID != ""; depth++ {
		if depth >= 64 || seen[original.RunID] {
			return nil, fmt.Errorf("invalid continuation ancestry")
		}
		seen[original.RunID] = true
		var err error
		original, err = store.GetRun(ctx, record.Principal, record.ThreadID, original.PriorRunID)
		if err != nil {
			return nil, err
		}
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
	if parent.Principal != record.Principal || parent.TurnID != inv.ParentTurnID || parent.ConversationID != inv.ParentConversationID {
		return nil, fmt.Errorf("detached parent protocol scope mismatch")
	}
	return clienttool.WithDetachedContinuation(ctx, clienttool.DetachedContinuation{ChildConversationID: original.ConversationID, ChildTurnID: original.TurnID, ParentConversationID: parent.ConversationID, ParentTurnID: parent.TurnID, ParentProtocolRunID: parent.RunID, ExecutionMode: "detach"}), nil
}
