package agent

import (
	"context"
	"testing"

	agentmdl "github.com/viant/agently-core/protocol/agent"
)

type testFinder struct {
	items map[string]*agentmdl.Agent
}

func (f *testFinder) Find(_ context.Context, name string) (*agentmdl.Agent, error) {
	if f == nil || f.items == nil {
		return nil, nil
	}
	if a, ok := f.items[name]; ok {
		return a, nil
	}
	return nil, nil
}

func TestResolveAgentIDForConversation_AutoCapabilityFallback(t *testing.T) {
	svc := &Service{
		agentFinder: &testFinder{items: map[string]*agentmdl.Agent{
			"agent_selector": {Identity: agentmdl.Identity{ID: "agent_selector", Name: "Agent Selector"}, Internal: false},
		}},
	}

	selected, auto, _, err := svc.resolveAgentIDForConversation(context.Background(), nil, "what can you do agent?")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !auto {
		t.Fatalf("expected auto selection")
	}
	if selected != "agent_selector" {
		t.Fatalf("unexpected selected id: %s", selected)
	}
}

func TestResolveAgentIDForConversation_AutoCapabilityFallbackSkipsInternal(t *testing.T) {
	svc := &Service{
		agentFinder: &testFinder{items: map[string]*agentmdl.Agent{
			"agent_selector": {Identity: agentmdl.Identity{ID: "agent_selector", Name: "Agent Selector"}, Internal: true},
		}},
	}

	_, _, _, err := svc.resolveAgentIDForConversation(context.Background(), nil, "what can you do agent?")
	if err == nil {
		t.Fatalf("expected error when only internal selector is available")
	}
}
