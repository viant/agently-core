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

	selected, auto, reason, err := svc.resolveAgentIDForConversation(context.Background(), nil, "", "what can you do agent?", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !auto {
		t.Fatalf("expected auto selection")
	}
	if selected != "agent_selector" {
		t.Fatalf("unexpected selected id: %s", selected)
	}
	if reason != "capability_direct" {
		t.Fatalf("unexpected routing reason: %s", reason)
	}
}

func TestResolveAgentIDForConversation_AutoCapabilityFallbackSkipsInternal(t *testing.T) {
	svc := &Service{
		agentFinder: &testFinder{items: map[string]*agentmdl.Agent{
			"agent_selector": {Identity: agentmdl.Identity{ID: "agent_selector", Name: "Agent Selector"}, Internal: true},
		}},
	}

	selected, auto, reason, err := svc.resolveAgentIDForConversation(context.Background(), nil, "", "what can you do agent?", "")
	if err != nil {
		t.Fatalf("unexpected error when synthetic selector fallback should be used: %v", err)
	}
	if !auto {
		t.Fatalf("expected auto selection")
	}
	if selected != "agent_selector" {
		t.Fatalf("unexpected selected id: %s", selected)
	}
	if reason != "capability_direct" {
		t.Fatalf("unexpected routing reason: %s", reason)
	}
}

func TestResolveAgentIDForConversation_ExplicitAutoSelectsCoderFromPublishedCatalog(t *testing.T) {
	svc := &Service{
		agentFinder: &allAgentFinder{
			items: []*agentmdl.Agent{
				{
					Identity:    agentmdl.Identity{ID: "coder", Name: "Coder"},
					Description: "Code agent",
					Profile: &agentmdl.Profile{
						Publish:     true,
						Name:        "Coder",
						Description: "Repository analysis, debugging, code changes, and build fixes",
					},
				},
				{
					Identity:    agentmdl.Identity{ID: "chatter", Name: "Chatter"},
					Description: "General chat agent",
					Profile: &agentmdl.Profile{
						Publish:     true,
						Name:        "Chatter",
						Description: "General conversation and everyday Q&A",
					},
				},
			},
		},
	}

	selected, auto, reason, err := svc.resolveAgentIDForConversation(context.Background(), nil, "auto", "repository analysis debugging build fixes", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !auto {
		t.Fatalf("expected auto selection")
	}
	if selected != "coder" {
		t.Fatalf("unexpected selected id: %s", selected)
	}
	if reason != "token_match" {
		t.Fatalf("unexpected routing reason: %s", reason)
	}
}

func TestResolveAgentIDForConversation_ExplicitAutoSelectsChatterFromPublishedCatalog(t *testing.T) {
	svc := &Service{
		agentFinder: &allAgentFinder{
			items: []*agentmdl.Agent{
				{
					Identity:    agentmdl.Identity{ID: "coder", Name: "Coder"},
					Description: "Code agent",
					Profile: &agentmdl.Profile{
						Publish:     true,
						Name:        "Coder",
						Description: "Repository analysis, debugging, code changes, and build fixes",
					},
				},
				{
					Identity:    agentmdl.Identity{ID: "chatter", Name: "Chatter"},
					Description: "General chat agent",
					Profile: &agentmdl.Profile{
						Publish:     true,
						Name:        "Chatter",
						Description: "General conversation, casual chat, concise guidance, and everyday Q&A",
					},
				},
			},
		},
	}

	selected, auto, reason, err := svc.resolveAgentIDForConversation(context.Background(), nil, "auto", "general conversation casual guidance", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !auto {
		t.Fatalf("expected auto selection")
	}
	if selected != "chatter" {
		t.Fatalf("unexpected selected id: %s", selected)
	}
	if reason != "token_match" {
		t.Fatalf("unexpected routing reason: %s", reason)
	}
}
