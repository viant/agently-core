package agent

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/viant/agently-core/genai/llm"
	agproto "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/prompt"
	"github.com/viant/agently-core/protocol/tool"
)

type allAgentFinder struct {
	items []*agproto.Agent
}

func (f *allAgentFinder) Find(ctx context.Context, id string) (*agproto.Agent, error) {
	for _, item := range f.items {
		if item != nil && strings.TrimSpace(item.ID) == strings.TrimSpace(id) {
			return item, nil
		}
	}
	return nil, nil
}

func (f *allAgentFinder) All() []*agproto.Agent { return f.items }

type staticRegistry struct {
	result string
}

func (s *staticRegistry) Definitions() []llm.ToolDefinition                { return nil }
func (s *staticRegistry) MatchDefinition(string) []*llm.ToolDefinition     { return nil }
func (s *staticRegistry) GetDefinition(string) (*llm.ToolDefinition, bool) { return nil, false }
func (s *staticRegistry) MustHaveTools([]string) ([]llm.Tool, error)       { return nil, nil }
func (s *staticRegistry) SetDebugLogger(io.Writer)                         {}
func (s *staticRegistry) Initialize(context.Context)                       {}
func (s *staticRegistry) Execute(context.Context, string, map[string]interface{}) (string, error) {
	return s.result, nil
}

var _ tool.Registry = (*staticRegistry)(nil)

func TestAppendAgentDirectoryDoc_UsesFinderWithoutRegistryExecution(t *testing.T) {
	svc := &Service{
		agentFinder: &allAgentFinder{
			items: []*agproto.Agent{
				{
					Identity:    agproto.Identity{ID: "coder", Name: "Coder"},
					Description: "Code agent",
					Profile: &agproto.Profile{
						Publish:     true,
						Name:        "Coder",
						Description: "Repository analysis and changes",
					},
				},
				{
					Identity: agproto.Identity{ID: "hidden", Name: "Hidden"},
					Profile:  &agproto.Profile{Publish: false, Name: "Hidden"},
				},
			},
		},
	}
	input := &QueryInput{
		Agent: &agproto.Agent{
			Identity: agproto.Identity{ID: "coder"},
			Delegation: &agproto.Delegation{
				Enabled: true,
			},
		},
	}
	docs := &prompt.Documents{}

	svc.appendAgentDirectoryDoc(context.Background(), input, docs)

	if assert.Len(t, docs.Items, 1) {
		assert.Equal(t, "internal://llm/agents/list", docs.Items[0].SourceURI)
		assert.Contains(t, docs.Items[0].PageContent, "Coder")
		assert.NotContains(t, docs.Items[0].PageContent, "Hidden")
	}
}

func TestAppendAgentDirectoryDoc_FallsBackToRegistryWhenFinderCacheEmpty(t *testing.T) {
	svc := &Service{
		agentFinder: &allAgentFinder{},
		registry: &staticRegistry{
			result: `{"items":[{"id":"coder","name":"Coder","summary":"Repository analysis and code changes"},{"id":"hidden","name":"Hidden","internal":true,"summary":"Internal only"}]}`,
		},
	}
	input := &QueryInput{
		Agent: &agproto.Agent{
			Identity: agproto.Identity{ID: "agent_selector"},
		},
	}
	docs := &prompt.Documents{}

	svc.appendAgentDirectoryDoc(context.Background(), input, docs)

	if assert.Len(t, docs.Items, 1) {
		assert.Contains(t, docs.Items[0].PageContent, "Coder (`coder`): Repository analysis and code changes")
		assert.NotContains(t, docs.Items[0].PageContent, "Hidden")
	}
}
