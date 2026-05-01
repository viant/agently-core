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

// NOTE: TestResolveAgentIDForConversation_AutoCapabilityFallback and its
// SkipsInternal variant were removed when the heuristic
// `isCapabilityDiscoveryQuery` shortcut was deleted. Capability-question
// detection now lives entirely inside the workspace-intake LLM router
// (agent_classifier.classifyAgentIDWithLLM), which produces a structured
// {action: "route" | "answer" | "clarify"} output rather than relying on
// hardcoded marker strings. End-to-end tests of that LLM-driven decision
// belong in integration tests, not unit tests of resolveAgentIDForConversation.

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
