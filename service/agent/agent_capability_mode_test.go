package agent

import (
	"context"
	"testing"

	"github.com/viant/agently-core/app/executor/config"
	agentmdl "github.com/viant/agently-core/protocol/agent"
)

func TestEnsureAgent_UsesCapabilityAgentForAgentSelector(t *testing.T) {
	service := &Service{
		defaults: &config.Defaults{CapabilityPrompt: "Custom capability prompt"},
	}
	input := &QueryInput{
		AgentID:       "agent_selector",
		Query:         "what can you do?",
		ModelOverride: "openai_gpt-5.4",
	}

	if err := service.ensureAgent(context.Background(), input); err != nil {
		t.Fatalf("ensureAgent() error = %v", err)
	}
	if input.Agent == nil {
		t.Fatalf("expected synthetic capability agent")
	}
	if got := input.Agent.ID; got != "agent_selector" {
		t.Fatalf("agent id = %q, want %q", got, "agent_selector")
	}
	if input.Agent.Name != "Agent Selector" {
		t.Fatalf("agent name = %q, want %q", input.Agent.Name, "Agent Selector")
	}
	if input.Agent.SystemPrompt == nil || input.Agent.SystemPrompt.Text != "Custom capability prompt" {
		t.Fatalf("expected capability prompt override to be applied")
	}
	if input.ToolsAllowed != nil {
		t.Fatalf("toolsAllowed = %#v, want nil for direct capability response mode", input.ToolsAllowed)
	}
	if input.AutoSelectTools == nil || *input.AutoSelectTools {
		t.Fatalf("expected autoSelectTools=false for capability mode")
	}
	if !input.DisableChains {
		t.Fatalf("expected disableChains=true for capability mode")
	}
	if input.ModelOverride != "openai_gpt-5.4" {
		t.Fatalf("model override = %q, want %q", input.ModelOverride, "openai_gpt-5.4")
	}
	if input.Agent.Model != "openai_gpt4o_mini" {
		t.Fatalf("capability agent fallback model = %q, want %q", input.Agent.Model, "openai_gpt4o_mini")
	}
}

func TestNewCapabilityAgent_UsesDefaultModel(t *testing.T) {
	ag := newCapabilityAgent(&config.Defaults{
		Model:            "openai_gpt4o_mini",
		CapabilityPrompt: "Custom capability prompt",
	})
	if ag == nil {
		t.Fatalf("expected capability agent")
	}
	if ag.ModelSelection.Model != "openai_gpt4o_mini" {
		t.Fatalf("model = %q, want %q", ag.ModelSelection.Model, "openai_gpt4o_mini")
	}
}

func TestLoadResolvedAgent_UsesFinderForNonCapabilityAgents(t *testing.T) {
	service := &Service{
		agentFinder: &stubFinder{agents: map[string]*agentmdl.Agent{
			"coder": {Identity: agentmdl.Identity{ID: "coder", Name: "Coder"}},
		}},
	}

	ag, err := service.loadResolvedAgent(context.Background(), "coder")
	if err != nil {
		t.Fatalf("loadResolvedAgent() error = %v", err)
	}
	if ag == nil || ag.ID != "coder" {
		t.Fatalf("expected finder-backed agent, got %#v", ag)
	}
}

type stubFinder struct {
	agents map[string]*agentmdl.Agent
}

func (s *stubFinder) Find(_ context.Context, id string) (*agentmdl.Agent, error) {
	if ag, ok := s.agents[id]; ok {
		return ag, nil
	}
	return nil, nil
}
