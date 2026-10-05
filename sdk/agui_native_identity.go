package sdk

import (
	"context"
	"errors"
	"fmt"
	aguistore "github.com/viant/agently-core/app/store/agui"
	iauth "github.com/viant/agently-core/internal/auth"
	agentsvc "github.com/viant/agently-core/service/agent"
)

// Only the authenticated durable store resolves a public thread to a native
// conversation. Public thread bytes remain journal/transport identities.
func bindAGUINativeQuery(record *aguistore.Run, query *agentsvc.QueryInput) error {
	if record == nil || query == nil || record.ConversationID == "" || record.ThreadID == "" || record.TurnID == "" || record.Principal == "" {
		return fmt.Errorf("resolved AG-UI native execution identity is required")
	}
	if query.UserId != record.Principal || query.MessageID != record.TurnID || (query.ConversationID != record.ThreadID && query.ConversationID != record.ConversationID) {
		return fmt.Errorf("AG-UI native query identity mismatch")
	}
	query.ConversationID = record.ConversationID
	return nil
}

// Native callbacks resolve a known public binding through authenticated store
// evidence. An unbound native conversation may establish its exact native ID
// as a server-created public label on later admission.
func aguiWireThreadForConversation(ctx context.Context, store aguistore.Store, principal, conversationID string) (string, error) {
	if conversationID == "" || principal == "" || store == nil {
		return "", fmt.Errorf("native conversation scope is required")
	}
	resolver, ok := store.(aguistore.ConversationThreadResolver)
	if !ok {
		return "", fmt.Errorf("native conversation binding resolver unavailable")
	}
	thread, err := resolver.GetThreadByConversationID(ctx, principal, conversationID)
	if errors.Is(err, aguistore.ErrNotFound) {
		return conversationID, nil
	}
	if err != nil {
		return "", err
	}
	if thread == nil || thread.ConversationID != conversationID || thread.Principal != principal || thread.ThreadID == "" {
		return "", fmt.Errorf("native conversation binding mismatch")
	}
	return thread.ThreadID, nil
}

func (c *backendClient) aguiThreadReference(ctx context.Context, conversationID string) (*string, error) {
	if c == nil || c.goalInvoker == nil || conversationID == "" {
		return nil, nil
	}
	principal := iauth.EffectiveUserID(ctx)
	if principal == "" {
		return nil, nil
	}
	resolver, ok := c.aguiStore().(aguistore.ConversationThreadResolver)
	if !ok {
		return nil, fmt.Errorf("native conversation binding resolver unavailable")
	}
	thread, err := resolver.GetThreadByConversationID(ctx, principal, conversationID)
	if errors.Is(err, aguistore.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if thread == nil || thread.ProtocolOnly || thread.ConversationID != conversationID || thread.Principal != principal || thread.ThreadID == "" {
		return nil, nil
	}
	value := thread.ThreadID
	return &value, nil
}
