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
