package agent_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/executor/config"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/app/store/data"
	"github.com/viant/agently-core/genai/llm"
	base "github.com/viant/agently-core/genai/llm/provider/base"
	authctx "github.com/viant/agently-core/internal/auth"
	internalconv "github.com/viant/agently-core/internal/service/conversation"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/binding"
	mcpname "github.com/viant/agently-core/protocol/mcpname"
	delegated "github.com/viant/agently-core/protocol/tool/service/llm/agents"
	"github.com/viant/agently-core/runtime/clienttool"
	agentsvc "github.com/viant/agently-core/service/agent"
	"github.com/viant/agently-core/service/core"
	"github.com/viant/agently-core/service/shared/toolexec"
)

type nestedModelFinder struct {
	mu      sync.Mutex
	calls   map[string]int
	results map[string][]llm.Message
}

func (f *nestedModelFinder) Find(_ context.Context, name string) (llm.Model, error) {
	return nestedModel{f, name}, nil
}

type nestedModel struct {
	finder *nestedModelFinder
	name   string
}

func (m nestedModel) Implements(feature string) bool { return feature == base.CanUseTools }
func (m nestedModel) Generate(_ context.Context, request *llm.GenerateRequest) (*llm.GenerateResponse, error) {
	m.finder.mu.Lock()
	defer m.finder.mu.Unlock()
	m.finder.calls[m.name]++
	count := m.finder.calls[m.name]
	message := llm.Message{Role: llm.RoleAssistant}
	if count == 1 {
		if m.name == "parent-model" {
			message.ToolCalls = []llm.ToolCall{{ID: "delegate-call", Name: "llm/agents:run", Arguments: map[string]interface{}{"agentId": "child", "objective": "lookup"}}}
		} else {
			message.ToolCalls = []llm.ToolCall{{ID: "frontend-leaf", Name: "browser_search", Arguments: map[string]interface{}{"q": "lookup"}}}
		}
	} else {
		m.finder.results[m.name] = request.Messages
		if m.name == "parent-model" {
			message.Content = "parent finished"
		} else {
			message.Content = "child finished"
		}
	}
	return &llm.GenerateResponse{Model: m.name, Choices: []llm.Choice{{Index: 0, Message: message, FinishReason: "stop"}}}, nil
}

type nestedAgentFinder struct{ agents map[string]*agentmdl.Agent }

func (f nestedAgentFinder) Find(_ context.Context, name string) (*agentmdl.Agent, error) {
	agent := f.agents[name]
	if agent == nil {
		return nil, fmt.Errorf("unknown agent %s", name)
	}
	return agent, nil
}
func (f nestedAgentFinder) All() []*agentmdl.Agent {
	var result []*agentmdl.Agent
	for _, agent := range f.agents {
		result = append(result, agent)
	}
	return result
}

type nestedRegistry struct {
	delegate *delegated.Service
	calls    int
}

func (r *nestedRegistry) Definitions() []llm.ToolDefinition {
	return []llm.ToolDefinition{{Name: "llm/agents:run", Description: "delegate", Parameters: map[string]interface{}{"type": "object"}}}
}
func (r *nestedRegistry) MatchDefinition(pattern string) []*llm.ToolDefinition {
	if pattern == "*" || mcpname.Canonical(pattern) == "llm_agents-run" {
		definition := r.Definitions()[0]
		return []*llm.ToolDefinition{&definition}
	}
	return nil
}
func (r *nestedRegistry) GetDefinition(name string) (*llm.ToolDefinition, bool) {
	defs := r.MatchDefinition(name)
	if len(defs) == 0 {
		return nil, false
	}
	return defs[0], true
}
func (r *nestedRegistry) MustHaveTools(patterns []string) ([]llm.Tool, error) {
	var result []llm.Tool
	for _, pattern := range patterns {
		for _, definition := range r.MatchDefinition(pattern) {
			result = append(result, llm.Tool{Definition: *definition})
		}
	}
	return result, nil
}
func (r *nestedRegistry) SetDebugLogger(io.Writer)   {}
func (r *nestedRegistry) Initialize(context.Context) {}
func (r *nestedRegistry) Execute(ctx context.Context, name string, args map[string]interface{}) (string, error) {
	if mcpname.Canonical(name) != "llm_agents-run" {
		return "", fmt.Errorf("frontend handler must not execute on backend: %s", name)
	}
	r.calls++
	raw, err := json.Marshal(args)
	if err != nil {
		return "", err
	}
	var input delegated.RunInput
	if err = json.Unmarshal(raw, &input); err != nil {
		return "", err
	}
	var output delegated.RunOutput
	method, err := r.delegate.Method("run")
	if err != nil {
		return "", err
	}
	if err = method(ctx, &input, &output); err != nil {
		return "", err
	}
	raw, err = json.Marshal(output)
	return string(raw), err
}

func TestNestedFrontendWaitResumesLeafThenOriginalDelegationThenParent(t *testing.T) {
	t.Setenv("AGENTLY_DB_DRIVER", "")
	t.Setenv("AGENTLY_DB_DSN", "")
	t.Setenv("AGENTLY_DB_PATH", "")
	workspace := t.TempDir()
	t.Setenv("AGENTLY_WORKSPACE", workspace)
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "devuser"})
	runtime, err := data.NewRuntimeFromWorkspace(ctx, workspace)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, runtime.Shutdown(context.Background())) })
	conversation, err := internalconv.New(ctx, runtime)
	require.NoError(t, err)
	native := data.NewService(runtime)
	row := apiconv.NewConversation()
	row.SetId("nested-parent")
	row.SetVisibility("private")
	require.NoError(t, conversation.PatchConversations(ctx, row))
	parent := &agentmdl.Agent{Identity: agentmdl.Identity{ID: "parent"}, ModelSelection: llm.ModelSelection{Model: "parent-model"}, Prompt: &binding.Prompt{Text: "delegate"}}
	child := &agentmdl.Agent{Identity: agentmdl.Identity{ID: "child"}, ModelSelection: llm.ModelSelection{Model: "child-model"}, Prompt: &binding.Prompt{Text: "lookup"}}
	finder := &nestedModelFinder{calls: map[string]int{}, results: map[string][]llm.Message{}}
	registry := &nestedRegistry{}
	llmService := core.New(finder, nil, conversation)
	service := agentsvc.New(llmService, nestedAgentFinder{agents: map[string]*agentmdl.Agent{"parent": parent, "child": child}}, nil, registry, &config.Defaults{}, conversation, agentsvc.WithDataService(native))
	registry.delegate = delegated.New(service, delegated.WithConversationClient(conversation), delegated.WithDataService(native))
	definitions := []llm.ToolDefinition{{Name: "browser_search", Description: "browser lookup", Parameters: map[string]interface{}{"type": "object"}}}
	session, err := clienttool.NewSession(definitions)
	require.NoError(t, err)
	ctx = clienttool.WithSession(ctx, session)
	output := &agentsvc.QueryOutput{}
	require.NoError(t, service.Query(ctx, &agentsvc.QueryInput{ConversationID: "nested-parent", MessageID: "parent-turn", AgentID: "parent", UserId: "devuser", Query: "lookup", ToolsAllowed: []string{"llm/agents:run"}}, output))
	require.Equal(t, "waiting_for_user", output.ExecutionStatus)
	require.Len(t, output.ClientToolCalls, 1)
	require.Len(t, output.ClientToolDependencies, 1)
	require.Equal(t, 1, registry.calls)
	dependency := output.ClientToolDependencies[0]
	leaf := output.ClientToolCalls[0]
	require.Equal(t, dependency.ChildTurnID, leaf.TurnID)
	require.NotEqual(t, "parent-turn", leaf.TurnID)
	message, err := conversation.GetMessage(ctx, dependency.ParentCall.ToolMessageID, apiconv.WithIncludeToolCall(true))
	require.NoError(t, err)
	require.Equal(t, "waiting_for_user", message.MessageToolCall.Status)
	require.Nil(t, message.MessageToolCall.CompletedAt)
	require.Nil(t, message.MessageToolCall.ResponsePayloadId)
	require.NoError(t, toolexec.CompleteClientToolResult(ctx, conversation, leaf, json.RawMessage(`"browser result"`), ""))
	childSession, err := clienttool.NewSession(definitions)
	require.NoError(t, err)
	childOutput := &agentsvc.QueryOutput{}
	require.NoError(t, service.ResumeContinuation(clienttool.WithContinuationDependency(clienttool.WithSession(ctx, childSession), dependency), &agentsvc.QueryInput{ConversationID: leaf.ConversationID, UserId: "devuser"}, leaf.TurnID, 0, childOutput))
	require.NoError(t, toolexec.CompleteDependency(ctx, conversation, dependency, clienttool.ChildResult{Content: childOutput.Content, Status: childOutput.ExecutionStatus, ConversationID: leaf.ConversationID, TurnID: leaf.TurnID}))
	parentSession, err := clienttool.NewSession(definitions)
	require.NoError(t, err)
	parentOutput := &agentsvc.QueryOutput{}
	require.NoError(t, service.ResumeContinuation(clienttool.WithSession(ctx, parentSession), &agentsvc.QueryInput{ConversationID: "nested-parent", UserId: "devuser"}, "parent-turn", 0, parentOutput))
	require.Equal(t, "parent finished", parentOutput.Content)
	require.Equal(t, 1, registry.calls)
	require.Equal(t, 2, finder.calls["parent-model"])
	require.Equal(t, 2, finder.calls["child-model"])
	var sawLeaf, sawParent bool
	for _, message := range finder.results["child-model"] {
		if message.Role == llm.RoleTool && message.ToolCallId == "frontend-leaf" && strings.Contains(message.Content, "browser result") {
			sawLeaf = true
		}
	}
	for _, message := range finder.results["parent-model"] {
		if message.Role == llm.RoleTool && message.ToolCallId == "delegate-call" && strings.Contains(message.Content, "child finished") {
			sawParent = true
		}
	}
	require.True(t, sawLeaf)
	require.True(t, sawParent)
}
