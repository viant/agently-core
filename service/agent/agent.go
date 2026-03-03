
package agent

import (
	"context"
	"fmt"
	"strings"

	apiconv "github.com/viant/agently-core/app/store/conversation"
)

// ensureAgent populates qi.Agent (using finder when needed) and echoes it on
// qo.Agent for caller convenience.
func (s *Service) ensureAgent(ctx context.Context, qi *QueryInput) error {
	if qi.Agent == nil {
		agentID := strings.TrimSpace(qi.AgentID)
		if agentID == "" || isAutoAgentRef(agentID) {
			var conv *apiconv.Conversation
			if s != nil && s.conversation != nil && strings.TrimSpace(qi.ConversationID) != "" {
				loaded, err := s.conversation.GetConversation(ctx, qi.ConversationID)
				if err != nil {
					return fmt.Errorf("failed to load conversation %q: %w", strings.TrimSpace(qi.ConversationID), err)
				}
				conv = loaded
			}
			selectedID, _, _, err := s.resolveAgentIDForConversation(ctx, conv, qi.Query)
			if err != nil {
				return fmt.Errorf("failed to resolve agent: %w", err)
			}
			agentID = strings.TrimSpace(selectedID)
			qi.AgentID = agentID
		}
		if agentID != "" {
			a, err := s.agentFinder.Find(ctx, agentID)
			if err != nil {
				return fmt.Errorf("failed to load agent: %w", err)
			}
			qi.Agent = a
		}
	}
	if qi.Agent == nil {
		return fmt.Errorf("agent is required")
	}
	return nil
}
