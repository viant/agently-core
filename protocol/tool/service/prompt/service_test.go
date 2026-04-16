package prompt

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	promptrepo "github.com/viant/agently-core/workspace/repository/prompt"
	fsstore "github.com/viant/agently-core/workspace/store/fs"
)

type promptTestFinder struct {
	agent *agentmdl.Agent
}

func (f *promptTestFinder) Find(context.Context, string) (*agentmdl.Agent, error) {
	return f.agent, nil
}

type promptTestConversationClient struct {
	conversation *apiconv.Conversation
	messages     []*apiconv.MutableMessage
}

func (c *promptTestConversationClient) GetConversation(context.Context, string, ...apiconv.Option) (*apiconv.Conversation, error) {
	return c.conversation, nil
}

func (c *promptTestConversationClient) GetConversations(context.Context, *apiconv.Input) ([]*apiconv.Conversation, error) {
	return nil, nil
}

func (c *promptTestConversationClient) PatchConversations(context.Context, *apiconv.MutableConversation) error {
	return nil
}

func (c *promptTestConversationClient) GetPayload(context.Context, string) (*apiconv.Payload, error) {
	return nil, nil
}

func (c *promptTestConversationClient) PatchPayload(context.Context, *apiconv.MutablePayload) error {
	return nil
}

func (c *promptTestConversationClient) PatchMessage(_ context.Context, message *apiconv.MutableMessage) error {
	c.messages = append(c.messages, message)
	return nil
}

func (c *promptTestConversationClient) GetMessage(context.Context, string, ...apiconv.Option) (*apiconv.Message, error) {
	return nil, nil
}

func (c *promptTestConversationClient) GetMessageByElicitation(context.Context, string, string) (*apiconv.Message, error) {
	return nil, nil
}

func (c *promptTestConversationClient) PatchModelCall(context.Context, *apiconv.MutableModelCall) error {
	return nil
}

func (c *promptTestConversationClient) PatchToolCall(context.Context, *apiconv.MutableToolCall) error {
	return nil
}

func (c *promptTestConversationClient) PatchTurn(context.Context, *apiconv.MutableTurn) error {
	return nil
}

func (c *promptTestConversationClient) DeleteConversation(context.Context, string) error {
	return nil
}

func (c *promptTestConversationClient) DeleteMessage(context.Context, string, string) error {
	return nil
}

func TestService_list_AllowsProfilesByDirectID(t *testing.T) {
	repo := promptrepo.NewWithStore(fsstore.New("/Users/awitas/go/src/github.com/viant/agently-core/workspace/repository/prompt/testdata"))
	agentID := "analyst"
	client := &promptTestConversationClient{
		conversation: &apiconv.Conversation{AgentId: &agentID},
	}
	finder := &promptTestFinder{
		agent: &agentmdl.Agent{
			Identity: agentmdl.Identity{ID: "analyst"},
			Prompts: agentmdl.PromptAccess{
				Bundles: []string{"performance_analysis"},
			},
		},
	}
	service := New(repo, WithConversationClient(client), WithAgentFinder(finder))
	ctx := runtimerequestctx.WithConversationID(context.Background(), "conv-1")

	out := &ListOutput{}
	err := service.list(ctx, &ListInput{}, out)
	require.NoError(t, err)
	require.Len(t, out.Profiles, 1)
	assert.Equal(t, "performance_analysis", out.Profiles[0].ID)
}

func TestService_get_InjectsRolePreservingMessages(t *testing.T) {
	repo := promptrepo.NewWithStore(fsstore.New("/Users/awitas/go/src/github.com/viant/agently-core/workspace/repository/prompt/testdata"))
	agentID := "analyst"
	client := &promptTestConversationClient{
		conversation: &apiconv.Conversation{AgentId: &agentID},
	}
	finder := &promptTestFinder{
		agent: &agentmdl.Agent{
			Identity: agentmdl.Identity{ID: "analyst"},
			Prompts: agentmdl.PromptAccess{
				Bundles: []string{"performance_analysis"},
			},
		},
	}
	service := New(repo, WithConversationClient(client), WithAgentFinder(finder))
	include := true
	ctx := runtimerequestctx.WithConversationID(context.Background(), "conv-1")
	ctx = runtimerequestctx.WithTurnMeta(ctx, runtimerequestctx.TurnMeta{
		ConversationID: "conv-1",
		TurnID:         "turn-1",
	})

	out := &GetOutput{}
	err := service.get(ctx, &GetInput{ID: "performance_analysis", IncludeDocument: &include}, out)
	require.NoError(t, err)
	assert.True(t, out.Injected)
	require.Len(t, out.Messages, 2)
	require.Len(t, client.messages, 2)

	assert.Equal(t, "system", client.messages[0].Role)
	assert.Equal(t, "system_document", valueOrEmpty(client.messages[0].Mode))
	assert.Equal(t, "system_doc", valueOrEmpty(client.messages[0].Tags))
	assert.Equal(t, "You are a performance analyst.\nFocus on KPI health, and concise evidence-backed observations.", valueOrEmpty(client.messages[0].Content))

	assert.Equal(t, "user", client.messages[1].Role)
	assert.Empty(t, valueOrEmpty(client.messages[1].Mode))
	assert.Empty(t, valueOrEmpty(client.messages[1].Tags))
	assert.Equal(t, "Analyze the campaign hierarchy.", valueOrEmpty(client.messages[1].Content))
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
