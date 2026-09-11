package provider

import (
	"context"
	"testing"

	"github.com/viant/agently-core/genai/llm/provider/base"
	"github.com/viant/agently-core/genai/llm/provider/vertexai/gemini"
)

func TestFactoryGeminiDisableStreaming(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "test-key")
	zero := 0
	model, err := New().CreateModel(context.Background(), &Options{
		Provider:         ProviderGeminiAI,
		Model:            "gemini-3.5-flash-lite",
		DisableStreaming: true,
		ThinkingBudget:   &zero,
	})
	if err != nil {
		t.Fatalf("create model: %v", err)
	}
	if model.Implements(base.CanStream) {
		t.Fatal("expected factory-created Gemini model to disable streaming")
	}
	client, ok := model.(*gemini.Client)
	if !ok {
		t.Fatalf("model type = %T, want *gemini.Client", model)
	}
	if client.ThinkingBudget == nil || *client.ThinkingBudget != 0 {
		t.Fatalf("thinking budget = %v, want explicit zero", client.ThinkingBudget)
	}
}
