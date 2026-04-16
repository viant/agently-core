package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	apiconv "github.com/viant/agently-core/app/store/conversation"
	convmem "github.com/viant/agently-core/app/store/data/memory"
	"github.com/viant/agently-core/genai/llm"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	toolbundle "github.com/viant/agently-core/protocol/tool/bundle"
	tplrepo "github.com/viant/agently-core/workspace/repository/template"
	fsstore "github.com/viant/agently-core/workspace/store/fs"
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

func TestService_BuildBinding_InjectsSelectedTemplateAndRemovesTemplateTools(t *testing.T) {
	store := convmem.New()
	ctx := context.Background()

	conversation := apiconv.NewConversation()
	conversation.SetId("conv-template")
	if err := store.PatchConversations(ctx, conversation); err != nil {
		t.Fatalf("patch conversation: %v", err)
	}
	message := apiconv.NewMessage()
	message.SetId("msg-template")
	message.SetConversationID("conv-template")
	message.SetTurnID("turn-template")
	message.SetRole("user")
	message.SetType("text")
	message.SetContent("render dashboard")
	if err := store.PatchMessage(ctx, message); err != nil {
		t.Fatalf("patch message: %v", err)
	}

	tmpDir := t.TempDir()
	templateDir := filepath.Join(tmpDir, "templates")
	if err := os.MkdirAll(templateDir, 0o755); err != nil {
		t.Fatalf("mkdir templates: %v", err)
	}
	templateBody := []byte("id: analytics_dashboard\nname: analytics_dashboard\ndescription: dashboard template\ninstructions: Return a dashboard.\n")
	if err := os.WriteFile(filepath.Join(templateDir, "analytics_dashboard.yaml"), templateBody, 0o644); err != nil {
		t.Fatalf("write template: %v", err)
	}

	service := &Service{
		conversation: store,
		registry: &fakeRegistry{defs: []llm.ToolDefinition{
			{Name: "template:get"},
			{Name: "template:list"},
			{Name: "system/os:getEnv"},
		}},
		toolBundles: func(context.Context) ([]*toolbundle.Bundle, error) {
			return []*toolbundle.Bundle{
				{ID: "template", Match: []llm.Tool{{Name: "template/*"}}},
				{ID: "system/os", Match: []llm.Tool{{Name: "system/os/*"}}},
			}, nil
		},
		templateRepo: tplrepo.NewWithStore(fsstore.New(tmpDir)),
	}
	binding, err := service.BuildBinding(ctx, &QueryInput{
		ConversationID: "conv-template",
		TemplateId:     "analytics_dashboard",
		Agent: &agentmdl.Agent{
			Identity:       agentmdl.Identity{ID: "agent-1"},
			ModelSelection: llm.ModelSelection{Model: "openai_gpt-5.2"},
			Tool:           agentmdl.Tool{Bundles: []string{"template", "system/os"}},
		},
		Query: "render dashboard",
	})
	if err != nil {
		t.Fatalf("BuildBinding error: %v", err)
	}
	foundTemplateDoc := false
	for _, doc := range binding.SystemDocuments.Items {
		if doc == nil {
			continue
		}
		if doc.SourceURI == "template://analytics_dashboard" {
			foundTemplateDoc = true
		}
	}
	if !foundTemplateDoc {
		t.Fatalf("expected injected template document, got %#v", binding.SystemDocuments.Items)
	}
	for _, sig := range binding.Tools.Signatures {
		if sig == nil {
			continue
		}
		if sig.Name == "template-get" || sig.Name == "template-list" || sig.Name == "template_get" || sig.Name == "template_list" {
			t.Fatalf("expected template tools to be removed after template injection, got %#v", binding.Tools.Signatures)
		}
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
