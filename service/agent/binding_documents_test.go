package agent

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/viant/agently-core/genai/llm"
	agproto "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/binding"
	"github.com/viant/agently-core/protocol/tool"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
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
	result   string
	calls    int
	lastName string
	lastArgs map[string]interface{}
}

func (s *staticRegistry) Definitions() []llm.ToolDefinition {
	return []llm.ToolDefinition{
		{Name: "llm/agents:list", Description: "list agents"},
		{Name: "llm/agents:topology", Description: "planner topology"},
		{Name: "llm/agents:tool_details", Description: "planner tool details"},
		{Name: "prompt:list", Description: "list prompt profiles"},
		{Name: "template:list", Description: "list templates"},
		{Name: "llm/skills:list", Description: "list visible skills"},
		{Name: "resources:roots", Description: "list resource roots"},
		{Name: "resources:list", Description: "list resources"},
		{Name: "resources:read", Description: "read a resource"},
		{Name: "resources:match", Description: "semantic resource match"},
	}
}
func (s *staticRegistry) MatchDefinition(name string) []*llm.ToolDefinition {
	if def, ok := s.GetDefinition(name); ok {
		return []*llm.ToolDefinition{def}
	}
	return nil
}
func (s *staticRegistry) GetDefinition(name string) (*llm.ToolDefinition, bool) {
	for _, def := range s.Definitions() {
		if strings.EqualFold(strings.TrimSpace(def.Name), strings.TrimSpace(name)) {
			copy := def
			return &copy, true
		}
	}
	return nil, false
}
func (s *staticRegistry) MustHaveTools([]string) ([]llm.Tool, error) { return nil, nil }
func (s *staticRegistry) SetDebugLogger(io.Writer)                   {}
func (s *staticRegistry) Initialize(context.Context)                 {}
func (s *staticRegistry) Execute(_ context.Context, name string, args map[string]interface{}) (string, error) {
	s.calls++
	s.lastName = name
	s.lastArgs = args
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
	docs := &binding.Documents{}

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
	docs := &binding.Documents{}

	svc.appendAgentDirectoryDoc(context.Background(), input, docs)

	if assert.Len(t, docs.Items, 1) {
		assert.Contains(t, docs.Items[0].PageContent, "Coder (`coder`): Repository analysis and code changes")
		assert.NotContains(t, docs.Items[0].PageContent, "Hidden")
	}
}

func TestAppendBootstrapSystemDocuments_ExecutesToolAndAddsProvenanceHeader(t *testing.T) {
	reg := &staticRegistry{result: `{"items":[{"id":"coder","name":"Coder"}]}`}
	svc := &Service{registry: reg}
	input := &QueryInput{
		ConversationID: "conv-1",
		Agent: &agproto.Agent{
			Identity: agproto.Identity{ID: "parent"},
			Bootstrap: agproto.Bootstrap{ToolCalls: []agproto.BootstrapToolCall{
				{
					ID:   "agent_directory",
					Tool: "llm/agents:list",
					Args: map[string]interface{}{"includeInternal": false},
					Inject: agproto.BootstrapInject{
						As:        "systemContext",
						Title:     "agents/directory",
						SourceURI: "internal://llm/agents/list",
					},
				},
			}},
		},
	}
	ctx := runtimerequestctx.WithTurnMeta(context.Background(), runtimerequestctx.TurnMeta{ConversationID: "conv-1", TurnID: "turn-1"})
	b := &binding.Binding{Tools: binding.Tools{Signatures: []*llm.ToolDefinition{{Name: "llm/agents:list"}}}}

	err := svc.appendBootstrapSystemDocuments(ctx, input, b)

	assert.NoError(t, err)
	assert.Equal(t, 1, reg.calls)
	assert.Equal(t, "llm/agents:list", reg.lastName)
	assert.Equal(t, false, reg.lastArgs["includeInternal"])
	if assert.Len(t, b.SystemDocuments.Items, 1) {
		doc := b.SystemDocuments.Items[0]
		assert.Equal(t, "agents/directory", doc.Title)
		assert.Equal(t, "internal://llm/agents/list", doc.SourceURI)
		assert.Equal(t, "bootstrap_tool_result", doc.Metadata["kind"])
		assert.Contains(t, doc.PageContent, "# Runtime Bootstrap Tool Result")
		assert.Contains(t, doc.PageContent, "Tool: `llm/agents:list`")
		assert.Contains(t, doc.PageContent, `"includeInternal": false`)
		assert.Contains(t, doc.PageContent, `"id":"coder"`)
	}
}

func TestAppendBootstrapSystemDocuments_CacheInheritsToolExposure_DataDriven(t *testing.T) {
	testCases := []struct {
		name          string
		exposure      agproto.ToolCallExposure
		turnIDs       []string
		expectedCalls int
	}{
		{
			name:          "turn exposure reuses within a turn",
			exposure:      agproto.ToolCallExposure("turn"),
			turnIDs:       []string{"turn-1", "turn-1"},
			expectedCalls: 1,
		},
		{
			name:          "turn exposure refreshes for the next turn",
			exposure:      agproto.ToolCallExposure("turn"),
			turnIDs:       []string{"turn-1", "turn-2"},
			expectedCalls: 2,
		},
		{
			name:          "conversation exposure reuses across turns",
			exposure:      agproto.ToolCallExposure("conversation"),
			turnIDs:       []string{"turn-1", "turn-2"},
			expectedCalls: 1,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			reg := &staticRegistry{result: `{"ok":true}`}
			svc := &Service{registry: reg}
			input := &QueryInput{
				ConversationID: "conv-1",
				Agent: &agproto.Agent{
					Identity: agproto.Identity{ID: "parent"},
					Tool:     agproto.Tool{CallExposure: testCase.exposure},
					Bootstrap: agproto.Bootstrap{ToolCalls: []agproto.BootstrapToolCall{
						{
							ID:   "agent_directory",
							Tool: "llm/agents:list",
							Args: map[string]interface{}{"includeInternal": false},
							Inject: agproto.BootstrapInject{
								As: "systemContext",
							},
						},
					}},
				},
			}

			for _, turnID := range testCase.turnIDs {
				ctx := runtimerequestctx.WithTurnMeta(context.Background(), runtimerequestctx.TurnMeta{ConversationID: "conv-1", TurnID: turnID})
				assert.NoError(t, svc.appendBootstrapSystemDocuments(ctx, input, &binding.Binding{
					Tools: binding.Tools{Signatures: []*llm.ToolDefinition{{Name: "llm/agents:list"}}},
				}))
			}

			assert.Equal(t, testCase.expectedCalls, reg.calls)
		})
	}
}

func TestAppendBootstrapSystemDocuments_DoesNotRequireVisibleToolSurface(t *testing.T) {
	reg := &staticRegistry{result: `{"ok":true}`}
	svc := &Service{registry: reg}
	input := &QueryInput{
		ConversationID: "conv-1",
		Agent: &agproto.Agent{
			Identity: agproto.Identity{ID: "parent"},
			Bootstrap: agproto.Bootstrap{ToolCalls: []agproto.BootstrapToolCall{
				{
					ID:   "planner_topology",
					Tool: "llm/agents:topology",
					Inject: agproto.BootstrapInject{
						As: "systemContext",
					},
				},
			}},
		},
	}
	ctx := runtimerequestctx.WithTurnMeta(context.Background(), runtimerequestctx.TurnMeta{ConversationID: "conv-1", TurnID: "turn-1"})
	b := &binding.Binding{
		Tools: binding.Tools{Signatures: []*llm.ToolDefinition{{Name: "llm/agents:list"}}},
	}

	err := svc.appendBootstrapSystemDocuments(ctx, input, b)

	assert.NoError(t, err)
	assert.Equal(t, 1, reg.calls)
	if assert.Len(t, b.SystemDocuments.Items, 1) {
		assert.Equal(t, "llm/agents:topology", reg.lastName)
		assert.Contains(t, b.SystemDocuments.Items[0].PageContent, `"ok":true`)
	}
}

func TestAppendBootstrapSystemDocuments_ExpandsArgsFromQueryInput(t *testing.T) {
	reg := &staticRegistry{result: `{"documents":[]}`}
	svc := &Service{registry: reg}
	input := &QueryInput{
		ConversationID: "conv-1",
		UserId:         "user-1",
		Query:          "recommend site changes for adOrder 2650500",
		Agent: &agproto.Agent{
			Identity: agproto.Identity{ID: "planner"},
			Bootstrap: agproto.Bootstrap{ToolCalls: []agproto.BootstrapToolCall{
				{
					ID:   "planner_match",
					Tool: "resources:match",
					Args: map[string]interface{}{
						"query":   "{{query}}",
						"rootIds": []interface{}{"planner-business-logic"},
					},
					Inject: agproto.BootstrapInject{
						As: "systemContext",
					},
				},
			}},
		},
	}
	ctx := runtimerequestctx.WithTurnMeta(context.Background(), runtimerequestctx.TurnMeta{ConversationID: "conv-1", TurnID: "turn-1", Assistant: "planner"})
	b := &binding.Binding{
		Tools: binding.Tools{Signatures: []*llm.ToolDefinition{{Name: "resources:match"}}},
	}

	err := svc.appendBootstrapSystemDocuments(ctx, input, b)

	assert.NoError(t, err)
	assert.Equal(t, "recommend site changes for adOrder 2650500", reg.lastArgs["query"])
	if assert.Len(t, b.SystemDocuments.Items, 1) {
		assert.Contains(t, b.SystemDocuments.Items[0].PageContent, "recommend site changes for adOrder 2650500")
	}
}
