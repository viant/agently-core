package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/viant/agently-core/app/executor/config"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	promptmdl "github.com/viant/agently-core/protocol/prompt"
	"github.com/viant/agently-core/service/agent/prompts"
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
			selectedID, autoSelected, routingReason, err := s.resolveAgentIDForConversation(ctx, conv, agentID, qi.Query)
			if err != nil {
				return fmt.Errorf("failed to resolve agent: %w", err)
			}
			agentID = strings.TrimSpace(selectedID)
			qi.AgentID = agentID
			qi.AutoSelected = autoSelected
			qi.RoutingReason = strings.TrimSpace(routingReason)
			infof("agent.ensureAgent resolved convo=%q selected=%q auto=%v reason=%q query_head=%q", strings.TrimSpace(qi.ConversationID), agentID, autoSelected, qi.RoutingReason, headString(qi.Query, 256))
		}
		if agentID != "" {
			a, err := s.loadResolvedAgent(ctx, agentID)
			if err != nil {
				return fmt.Errorf("failed to load agent: %w", err)
			}
			qi.Agent = a
			if isCapabilityAgentID(agentID) {
				autoTools := false
				qi.AutoSelectTools = &autoTools
				qi.ToolsAllowed = nil
				qi.DisableChains = true
				infof("agent.ensureAgent capability_mode convo=%q agent_id=%q", strings.TrimSpace(qi.ConversationID), agentID)
			}
		}
	}
	if qi.Agent == nil {
		return fmt.Errorf("agent is required")
	}
	return nil
}

func isCapabilityAgentID(agentID string) bool {
	agentID = strings.TrimSpace(agentID)
	return strings.EqualFold(agentID, "agent_selector") || strings.EqualFold(agentID, "agent-selector")
}

func (s *Service) loadResolvedAgent(ctx context.Context, agentID string) (*agentmdl.Agent, error) {
	if isCapabilityAgentID(agentID) {
		return newCapabilityAgent(s.defaults), nil
	}
	if s == nil || s.agentFinder == nil {
		return nil, fmt.Errorf("agent finder not configured")
	}
	return s.agentFinder.Find(ctx, agentID)
}

func newCapabilityAgent(defaults *config.Defaults) *agentmdl.Agent {
	capPrompt := prompts.CapabilityPrompt()
	if defaults != nil && strings.TrimSpace(defaults.CapabilityPrompt) != "" {
		capPrompt = strings.TrimSpace(defaults.CapabilityPrompt)
	}
	return &agentmdl.Agent{
		Identity: agentmdl.Identity{ID: "agent_selector", Name: "Agent Selector"},
		Prompt:   &promptmdl.Prompt{Text: "{{.Task.Prompt}}", Engine: "go"},
		SystemPrompt: &promptmdl.Prompt{
			Text:   capPrompt,
			Engine: "go",
		},
		Persona: &promptmdl.Persona{Role: "assistant", Actor: "Capability"},
	}
}
