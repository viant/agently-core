package agent

import (
	"context"
	"strings"
	"time"

	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/runtime/streaming"
)

func (s *Service) emitExpandedUserPromptEvent(ctx context.Context, conversationID, turnID, userMessageID, expandedPrompt string, createdAt time.Time) {
	if s == nil || s.streamPub == nil {
		return
	}
	conversationID = strings.TrimSpace(conversationID)
	turnID = strings.TrimSpace(turnID)
	userMessageID = strings.TrimSpace(userMessageID)
	expandedPrompt = strings.TrimSpace(expandedPrompt)
	if conversationID == "" || turnID == "" || userMessageID == "" || expandedPrompt == "" {
		return
	}
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	event := &streaming.Event{
		ID:             userMessageID,
		StreamID:       conversationID,
		ConversationID: conversationID,
		TurnID:         turnID,
		MessageID:      userMessageID,
		UserMessageID:  userMessageID,
		Type:           streaming.EventTypeUserPromptExpanded,
		Content:        expandedPrompt,
		CreatedAt:      createdAt,
	}
	if mode := strings.TrimSpace(runtimerequestctx.RequestModeFromContext(ctx)); mode != "" {
		event.Mode = mode
	}
	_ = s.streamPub.Publish(ctx, event)
}
