package agent

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/executor/config"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	convmem "github.com/viant/agently-core/app/store/data/memory"
	"github.com/viant/agently-core/genai/llm"
	base "github.com/viant/agently-core/genai/llm/provider/base"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/binding"
	"github.com/viant/agently-core/runtime/clienttool"
	memory "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/service/core"
	"github.com/viant/agently-core/service/reactor"
)

type frontendToolFinder struct {
	calls atomic.Int32
	tools []llm.Tool
}

func (f *frontendToolFinder) Find(context.Context, string) (llm.Model, error) {
	return frontendToolModel{f}, nil
}

type frontendToolModel struct{ finder *frontendToolFinder }

func (m frontendToolModel) Implements(feature string) bool { return feature == base.CanUseTools }
func (m frontendToolModel) Generate(_ context.Context, request *llm.GenerateRequest) (*llm.GenerateResponse, error) {
	m.finder.calls.Add(1)
	if request.Options != nil {
		m.finder.tools = request.Options.Tools
	}
	return &llm.GenerateResponse{Model: "mock", Choices: []llm.Choice{{Index: 0, Message: llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "frontend-call", Name: "frontend", Arguments: map[string]interface{}{"id": 7}}}}, FinishReason: "tool_calls"}}}, nil
}

func TestBuildBindingAddsScopedClientToolsAndRejectsBackendCollision(t *testing.T) {
	store := convmem.New()
	ctx := context.Background()
	conv := apiconv.NewConversation()
	conv.SetId("client-conv")
	require.NoError(t, store.PatchConversations(ctx, conv))
	svc := &Service{conversation: store, registry: &fakeRegistry{defs: []llm.ToolDefinition{{Name: "server/lookup"}}}}
	input := &QueryInput{ConversationID: "client-conv", Agent: &agentmdl.Agent{Identity: agentmdl.Identity{ID: "agent"}}}
	session, err := clienttool.NewSession([]llm.ToolDefinition{{Name: "frontend", Parameters: map[string]interface{}{"type": "object"}}})
	require.NoError(t, err)
	result, err := svc.BuildBinding(clienttool.WithSession(ctx, session), input)
	require.NoError(t, err)
	require.Contains(t, bindingToolNames(result.Tools.Signatures), "frontend")
	result, err = svc.BuildBinding(ctx, input)
	require.NoError(t, err)
	require.NotContains(t, bindingToolNames(result.Tools.Signatures), "frontend")
	collision, err := clienttool.NewSession([]llm.ToolDefinition{{Name: "server-lookup"}})
	require.NoError(t, err)
	_, err = svc.BuildBinding(clienttool.WithSession(ctx, collision), input)
	require.ErrorContains(t, err, "collides")
}

func TestClientToolRunStopsAfterOneModelIterationWithoutFakeResult(t *testing.T) {
	store := convmem.New()
	ctx := context.Background()
	conv := apiconv.NewConversation()
	conv.SetId("client-loop")
	require.NoError(t, store.PatchConversations(ctx, conv))
	turn := apiconv.NewTurn()
	turn.SetId("client-turn")
	turn.SetConversationID("client-loop")
	turn.SetStatus("running")
	require.NoError(t, store.PatchTurn(ctx, turn))
	finder := &frontendToolFinder{}
	llmSvc := core.New(finder, nil, store)
	svc := &Service{llm: llmSvc, conversation: store, defaults: &config.Defaults{}, orchestrator: reactor.New(llmSvc, nil, store, nil, nil)}
	input := &QueryInput{ConversationID: "client-loop", MessageID: "client-turn", UserId: "user", Query: "lookup", Agent: &agentmdl.Agent{Identity: agentmdl.Identity{ID: "agent"}, ModelSelection: llm.ModelSelection{Model: "mock"}, Prompt: &binding.Prompt{Text: "use frontend"}}}
	session, err := clienttool.NewSession([]llm.ToolDefinition{{Name: "frontend", Description: "lookup", Parameters: map[string]interface{}{"type": "object"}}})
	require.NoError(t, err)
	ctx = clienttool.WithSession(memory.WithTurnMeta(ctx, memory.TurnMeta{ConversationID: "client-loop", TurnID: "client-turn"}), session)
	output := &QueryOutput{}
	status, err := svc.runPlanAndStatus(ctx, input, output)
	require.NoError(t, err)
	require.Equal(t, "waiting_for_user", status)
	require.EqualValues(t, 1, finder.calls.Load())
	require.Len(t, output.ClientToolCalls, 1)
	require.Equal(t, "frontend-call", output.ClientToolCalls[0].ID)
	require.NotEmpty(t, output.ClientToolCalls[0].ToolMessageID)
	require.NotEmpty(t, finder.tools)
}
