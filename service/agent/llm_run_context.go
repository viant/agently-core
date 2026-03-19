package agent

import (
	"context"
	"strings"

	"github.com/google/uuid"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/runtime/memory"
)

// ensureRunTrackedLLMContext normalizes context so LLM calls participate in
// the same turn/run tracking model as the main query execution path.
func (s *Service) ensureRunTrackedLLMContext(ctx context.Context, conversationID, assistant, preferredTurnID string) context.Context {
	conversationID = strings.TrimSpace(conversationID)
	if conversationID != "" {
		ctx = memory.WithConversationID(ctx, conversationID)
	}

	turn, ok := memory.TurnMetaFromContext(ctx)
	if !ok {
		turn = memory.TurnMeta{}
	}
	if strings.TrimSpace(turn.TurnID) == "" {
		turnID := strings.TrimSpace(preferredTurnID)
		if turnID == "" {
			turnID = uuid.NewString()
		}
		turn.TurnID = turnID
	}
	if strings.TrimSpace(turn.ParentMessageID) == "" {
		turn.ParentMessageID = turn.TurnID
	}
	if strings.TrimSpace(turn.ConversationID) == "" {
		turn.ConversationID = conversationID
	}
	if strings.TrimSpace(turn.Assistant) == "" {
		turn.Assistant = strings.TrimSpace(assistant)
	}

	ctx = memory.WithTurnMeta(ctx, turn)
	// Ensure the turn exists for recorder-backed model/tool call persistence.
	if s != nil && s.conversation != nil && strings.TrimSpace(turn.ConversationID) != "" && strings.TrimSpace(turn.TurnID) != "" {
		rec := apiconv.NewTurn()
		rec.SetId(turn.TurnID)
		rec.SetConversationID(turn.ConversationID)
		rec.SetStatus("running")
		if strings.TrimSpace(assistant) != "" {
			rec.SetAgentIDUsed(strings.TrimSpace(assistant))
		}
		_ = s.conversation.PatchTurn(ctx, rec)
	}
	return ctx
}
