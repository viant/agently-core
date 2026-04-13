package agent

import (
	"context"
	"testing"

	apiconv "github.com/viant/agently-core/app/store/conversation"
	convmem "github.com/viant/agently-core/app/store/data/memory"
	"github.com/viant/agently-core/genai/llm"
	agentmdl "github.com/viant/agently-core/protocol/agent"
)

type captureConversationOptions struct {
	apiconv.Client
	last apiconv.Input
}

func (c *captureConversationOptions) GetConversation(ctx context.Context, id string, options ...apiconv.Option) (*apiconv.Conversation, error) {
	var input apiconv.Input
	for _, option := range options {
		if option != nil {
			option(&input)
		}
	}
	c.last = input
	return c.Client.GetConversation(ctx, id, options...)
}

func TestService_BuildBinding_FetchesTranscriptModelAndToolCalls(t *testing.T) {
	store := convmem.New()
	client := &captureConversationOptions{Client: store}
	ctx := context.Background()

	conversation := apiconv.NewConversation()
	conversation.SetId("conv-1")
	if err := store.PatchConversations(ctx, conversation); err != nil {
		t.Fatalf("patch conversation: %v", err)
	}
	message := apiconv.NewMessage()
	message.SetId("msg-1")
	message.SetConversationID("conv-1")
	message.SetTurnID("turn-1")
	message.SetRole("user")
	message.SetType("text")
	message.SetContent("hello")
	if err := store.PatchMessage(ctx, message); err != nil {
		t.Fatalf("patch message: %v", err)
	}

	service := &Service{conversation: client}
	_, err := service.BuildBinding(ctx, &QueryInput{
		ConversationID: "conv-1",
		Agent: &agentmdl.Agent{
			Identity:       agentmdl.Identity{ID: "agent-1"},
			ModelSelection: llm.ModelSelection{Model: "openai_gpt-5.2"},
		},
		Query: "hello",
	})
	if err != nil {
		t.Fatalf("BuildBinding error: %v", err)
	}
	if !client.last.IncludeToolCall {
		t.Fatalf("expected IncludeToolCall to be true")
	}
	if !client.last.IncludeModelCal {
		t.Fatalf("expected IncludeModelCal to be true")
	}
	if !client.last.IncludeTranscript {
		t.Fatalf("expected IncludeTranscript to be true")
	}
}

type panicConversationClient struct{}

func (p *panicConversationClient) GetConversation(context.Context, string, ...apiconv.Option) (*apiconv.Conversation, error) {
	panic("unexpected GetConversation call")
}

func (p *panicConversationClient) GetConversations(context.Context, *apiconv.Input) ([]*apiconv.Conversation, error) {
	return nil, nil
}

func (p *panicConversationClient) PatchConversations(context.Context, *apiconv.MutableConversation) error {
	return nil
}

func (p *panicConversationClient) GetPayload(context.Context, string) (*apiconv.Payload, error) {
	return nil, nil
}

func (p *panicConversationClient) PatchPayload(context.Context, *apiconv.MutablePayload) error {
	return nil
}

func (p *panicConversationClient) PatchMessage(context.Context, *apiconv.MutableMessage) error {
	return nil
}

func (p *panicConversationClient) GetMessage(context.Context, string, ...apiconv.Option) (*apiconv.Message, error) {
	return nil, nil
}

func (p *panicConversationClient) GetMessageByElicitation(context.Context, string, string) (*apiconv.Message, error) {
	return nil, nil
}

func (p *panicConversationClient) PatchModelCall(context.Context, *apiconv.MutableModelCall) error {
	return nil
}

func (p *panicConversationClient) PatchToolCall(context.Context, *apiconv.MutableToolCall) error {
	return nil
}

func (p *panicConversationClient) PatchTurn(context.Context, *apiconv.MutableTurn) error {
	return nil
}

func (p *panicConversationClient) DeleteConversation(context.Context, string) error {
	return nil
}

func (p *panicConversationClient) DeleteMessage(context.Context, string, string) error {
	return nil
}

func TestService_BuildBinding_SkipBindingConversationLoad(t *testing.T) {
	service := &Service{conversation: &panicConversationClient{}}
	binding, err := service.BuildBinding(WithFreshEmbeddedConversation(context.Background()), &QueryInput{
		ConversationID: "conv-1",
		Agent: &agentmdl.Agent{
			Identity:       agentmdl.Identity{ID: "agent-1"},
			ModelSelection: llm.ModelSelection{Model: "openai_gpt-5.2"},
		},
		Query: "hello",
	})
	if err != nil {
		t.Fatalf("BuildBinding error: %v", err)
	}
	if binding == nil {
		t.Fatalf("expected binding")
	}
	if got := len(binding.History.Messages); got != 0 {
		t.Fatalf("expected empty history, got %d messages", got)
	}
}
