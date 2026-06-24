package clienthandler

import (
	"context"
	"encoding/json"
	"testing"

	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/jsonrpc"
	mcpschema "github.com/viant/mcp-protocol/schema"
)

func TestSamplingImplementsOnlyWhenConfigured(t *testing.T) {
	if New(nil, nil).Implements(mcpschema.MethodSamplingCreateMessage) {
		t.Fatalf("sampling should be disabled without a model finder and default model")
	}
	handler := New(nil, nil, WithModelFinder(&fakeSamplingFinder{model: &fakeSamplingModel{}}), WithDefaultModel("test-model"))
	if !handler.Implements(mcpschema.MethodSamplingCreateMessage) {
		t.Fatalf("sampling should be enabled with a model finder and default model")
	}
}

func TestCreateMessageUsesConfiguredModel(t *testing.T) {
	model := &fakeSamplingModel{text: "generated diagnosis"}
	finder := &fakeSamplingFinder{model: model}
	handler := New(nil, nil, WithModelFinder(finder), WithDefaultModel("test-model"))
	systemPrompt := "system guidance"

	result, rpcErr := handler.CreateMessage(context.Background(), &jsonrpc.TypedRequest[*mcpschema.CreateMessageRequest]{
		Request: &mcpschema.CreateMessageRequest{
			Params: mcpschema.CreateMessageRequestParams{
				SystemPrompt: &systemPrompt,
				MaxTokens:    123,
				Messages: []mcpschema.SamplingMessage{{
					Role: mcpschema.RoleUser,
					Content: mcpschema.SamplingMessageContent{
						Type: "text",
						Text: "diagnose this",
					},
				}},
			},
		},
	})

	if rpcErr != nil {
		t.Fatalf("CreateMessage returned error: %v", rpcErr)
	}
	if finder.id != "test-model" {
		t.Fatalf("expected finder model id test-model, got %q", finder.id)
	}
	if model.request == nil {
		t.Fatalf("expected generate request")
	}
	if model.request.Instructions != systemPrompt {
		t.Fatalf("expected system prompt %q, got %q", systemPrompt, model.request.Instructions)
	}
	if model.request.Options == nil || model.request.Options.Model != "" || model.request.Options.MaxTokens != 123 {
		t.Fatalf("unexpected options: %#v", model.request.Options)
	}
	if got := llm.MessageText(model.request.Messages[0]); got != "diagnose this" {
		t.Fatalf("expected prompt text, got %q", got)
	}
	if result == nil || result.Content.Text != "generated diagnosis" || result.Model != "fake-model" {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestCreateMessageUsesModelPreferenceHint(t *testing.T) {
	model := &fakeSamplingModel{text: "generated diagnosis"}
	finder := &fakeSamplingFinder{model: model}
	handler := New(nil, nil, WithModelFinder(finder), WithDefaultModel("fallback-model"))
	hint := "gpt-5.5"
	intelligence := 0.99

	_, rpcErr := handler.CreateMessage(context.Background(), &jsonrpc.TypedRequest[*mcpschema.CreateMessageRequest]{
		Request: &mcpschema.CreateMessageRequest{
			Params: mcpschema.CreateMessageRequestParams{
				ModelPreferences: &mcpschema.ModelPreferences{
					IntelligencePriority: &intelligence,
					Hints: []mcpschema.ModelHint{{
						Name: &hint,
					}},
				},
				MaxTokens: 123,
				Messages: []mcpschema.SamplingMessage{{
					Role: mcpschema.RoleUser,
					Content: mcpschema.SamplingMessageContent{
						Type: "text",
						Text: "diagnose this",
					},
				}},
			},
		},
	})

	if rpcErr != nil {
		t.Fatalf("CreateMessage returned error: %v", rpcErr)
	}
	if finder.id != "gpt-5.5" {
		t.Fatalf("expected first hint model id gpt-5.5, got %q", finder.id)
	}
	if model.request.Options == nil || model.request.Options.Model != "" {
		t.Fatalf("sampling should not override provider model, got %#v", model.request.Options)
	}
}

func TestCreateMessageAuditsSamplingRequestAndResponse(t *testing.T) {
	model := &fakeSamplingModel{text: "generated diagnosis"}
	finder := &fakeSamplingFinder{model: model}
	conversations := &auditCaptureConversation{}
	handler := New(nil, conversations, WithModelFinder(finder), WithDefaultModel("test-model"))

	_, rpcErr := handler.CreateMessage(context.Background(), &jsonrpc.TypedRequest[*mcpschema.CreateMessageRequest]{
		Request: &mcpschema.CreateMessageRequest{
			Params: mcpschema.CreateMessageRequestParams{
				MaxTokens: 64000,
				Messages: []mcpschema.SamplingMessage{{
					Role: mcpschema.RoleUser,
					Content: mcpschema.SamplingMessageContent{
						Type: "text",
						Text: "diagnose this",
					},
				}},
			},
		},
	})

	if rpcErr != nil {
		t.Fatalf("CreateMessage returned error: %v", rpcErr)
	}
	if len(conversations.payloads) != 2 {
		t.Fatalf("expected request and response audit payloads, got %d", len(conversations.payloads))
	}
	if conversations.payloads[0].Kind != "model_request" || conversations.payloads[1].Kind != "model_response" {
		t.Fatalf("unexpected payload kinds: %q %q", conversations.payloads[0].Kind, conversations.payloads[1].Kind)
	}
	for _, payload := range conversations.payloads {
		if payload.Subtype == nil || *payload.Subtype != samplingAuditSubtype {
			t.Fatalf("expected sampling audit subtype, got %#v", payload.Subtype)
		}
		if payload.InlineBody == nil || len(*payload.InlineBody) == 0 {
			t.Fatalf("expected inline audit body")
		}
		var envelope samplingAuditPayload
		if err := json.Unmarshal(*payload.InlineBody, &envelope); err != nil {
			t.Fatalf("failed to unmarshal audit payload: %v", err)
		}
		if envelope.TraceID == "" || envelope.ModelID != "test-model" || envelope.Subtype != samplingAuditSubtype {
			t.Fatalf("unexpected audit envelope: %#v", envelope)
		}
	}
}

type fakeSamplingFinder struct {
	id    string
	model llm.Model
}

func (f *fakeSamplingFinder) Find(_ context.Context, id string) (llm.Model, error) {
	f.id = id
	return f.model, nil
}

type fakeSamplingModel struct {
	text    string
	request *llm.GenerateRequest
}

func (m *fakeSamplingModel) Generate(_ context.Context, request *llm.GenerateRequest) (*llm.GenerateResponse, error) {
	m.request = request
	return &llm.GenerateResponse{
		Model: "fake-model",
		Choices: []llm.Choice{{
			Message:      llm.NewAssistantMessage(m.text),
			FinishReason: "endTurn",
		}},
	}, nil
}

func (m *fakeSamplingModel) Implements(_ string) bool {
	return true
}

type auditCaptureConversation struct {
	apiconv.Client
	payloads []*apiconv.MutablePayload
}

func (c *auditCaptureConversation) PatchPayload(_ context.Context, payload *apiconv.MutablePayload) error {
	c.payloads = append(c.payloads, payload)
	return nil
}
