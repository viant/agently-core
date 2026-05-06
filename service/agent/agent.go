package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/viant/agently-core/internal/logx"
	"github.com/viant/agently-core/internal/textutil"

	"github.com/viant/agently-core/app/executor/config"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/genai/llm"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	promptmdl "github.com/viant/agently-core/protocol/binding"
	"github.com/viant/agently-core/service/agent/prompts"
	intakesvc "github.com/viant/agently-core/service/intake"
)

// PresetAssistantTextKey / PresetAssistantKindKey are the reserved
// QueryInput.Context keys carrying a workspace-intake preset assistant
// message produced by the classifier (action=answer or action=clarify).
//
// When PresetAssistantTextKey is non-empty and PresetAssistantKindKey is one
// of "answer" / "clarify", the runtime publishes the text as the turn's
// assistant message via the standard transcript writer and short-circuits
// without invoking the agent's LLM. This is the "ONE LLM call for capability
// turns" wire-up — the classifier's single LLM call already produced the
// authoritative text; running the agent again would be wasteful and would
// risk diverging output.
//
// Callers wanting to drive a preset directly (tests, integrations) may set
// these keys on QueryInput.Context themselves; the resolution logic in
// ensureAgent populates them automatically when the workspace-intake
// classifier returns action=answer or action=clarify.
const (
	PresetAssistantTextKey = "intake.preset.text"
	PresetAssistantKindKey = "intake.preset.kind"
)

// presetAssistantFromContext reads the preset assistant payload, if any.
// Returns text="" when no preset is present.
func presetAssistantFromContext(ctx map[string]any) (text, kind string) {
	if len(ctx) == 0 {
		return "", ""
	}
	if v, ok := ctx[PresetAssistantTextKey].(string); ok {
		text = v
	}
	if v, ok := ctx[PresetAssistantKindKey].(string); ok {
		kind = v
	}
	return text, kind
}

func applyWorkspaceTurnContext(qi *QueryInput, dec *routingDecision, agent *agentmdl.Agent) {
	if qi == nil || dec == nil || dec.WorkspaceTurnContext == nil {
		return
	}
	if qi.Context == nil {
		qi.Context = make(map[string]any)
	}
	copy := *dec.WorkspaceTurnContext
	if copy.Routing.Mode == intakesvc.ModePlanner && agent != nil && strings.TrimSpace(copy.Planner.AgentID) == "" {
		copy.Planner.AgentID = strings.TrimSpace(agent.Intake.PlannerAgentID)
	}
	qi.Context[intakesvc.ContextKey] = &copy
}

// ensureAgent populates qi.Agent (using finder when needed) and echoes it on
// qo.Agent for caller convenience.
func (s *Service) ensureAgent(ctx context.Context, qi *QueryInput) error {
	if qi.Agent == nil {
		agentID := strings.TrimSpace(qi.AgentID)
		var decision *routingDecision
		var err error
		if agentID == "" || isAutoAgentRef(agentID) {
			var conv *apiconv.Conversation
			if s != nil && s.conversation != nil && strings.TrimSpace(qi.ConversationID) != "" {
				loaded, err := s.conversation.GetConversation(ctx, qi.ConversationID)
				if err != nil {
					return fmt.Errorf("failed to load conversation %q: %w", strings.TrimSpace(qi.ConversationID), err)
				}
				conv = loaded
			}
			decision, err = s.resolveTurnRouting(ctx, conv, agentID, qi.Query, qi.MessageID)
			if err != nil {
				return fmt.Errorf("failed to resolve agent: %w", err)
			}
			if decision == nil {
				return fmt.Errorf("failed to resolve agent: nil decision")
			}
			agentID = strings.TrimSpace(decision.AgentID)
			qi.AgentID = agentID
			qi.AutoSelected = decision.AutoSelected
			qi.RoutingReason = strings.TrimSpace(decision.RoutingReason)
			// Stash classifier-produced preset (action=answer / action=clarify)
			// so the downstream publish-and-short-circuit can emit the text as
			// the assistant message without invoking a second LLM call. The
			// reserved keys are consumed by the message publisher in the
			// generate path; absent these keys, the agent runs normally.
			if decision.Preset != nil {
				if qi.Context == nil {
					qi.Context = make(map[string]any)
				}
				switch decision.Preset.Action {
				case ClassifierActionAnswer:
					qi.Context[PresetAssistantTextKey] = decision.Preset.Answer
					qi.Context[PresetAssistantKindKey] = ClassifierActionAnswer
				case ClassifierActionClarify:
					qi.Context[PresetAssistantTextKey] = decision.Preset.Question
					qi.Context[PresetAssistantKindKey] = ClassifierActionClarify
				}
			}
			logx.Infof("conversation", "agent.ensureAgent resolved convo=%q selected=%q auto=%v reason=%q query_head=%q preset=%v", strings.TrimSpace(qi.ConversationID), agentID, decision.AutoSelected, qi.RoutingReason, textutil.Head(qi.Query, 256), decision.Preset != nil)
		}
		if agentID != "" {
			a, err := s.loadResolvedAgent(ctx, agentID)
			if err != nil {
				return fmt.Errorf("failed to load agent: %w", err)
			}
			qi.Agent = a
			applyWorkspaceTurnContext(qi, decision, a)
			if isCapabilityAgentID(agentID) {
				autoTools := false
				qi.AutoSelectTools = &autoTools
				qi.ToolsAllowed = nil
				qi.DisableChains = true
				logx.Infof("conversation", "agent.ensureAgent capability_mode convo=%q agent_id=%q", strings.TrimSpace(qi.ConversationID), agentID)
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
	modelID := ""
	const fallbackCapabilityModel = "openai_gpt4o_mini"
	if defaults != nil && strings.TrimSpace(defaults.CapabilityPrompt) != "" {
		capPrompt = strings.TrimSpace(defaults.CapabilityPrompt)
	}
	if defaults != nil {
		modelID = strings.TrimSpace(defaults.Model)
	}
	if modelID == "" {
		modelID = fallbackCapabilityModel
	}
	return &agentmdl.Agent{
		Identity:       agentmdl.Identity{ID: "agent_selector", Name: "Agent Selector"},
		ModelSelection: llm.ModelSelection{Model: modelID},
		Prompt:         &promptmdl.Prompt{Text: "{{.Task.Prompt}}", Engine: "go"},
		SystemPrompt: &promptmdl.Prompt{
			Text:   capPrompt,
			Engine: "go",
		},
		Persona: &promptmdl.Persona{Role: "assistant", Actor: "Capability"},
	}
}
