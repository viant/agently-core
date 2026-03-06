package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	agproto "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/prompt"
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
