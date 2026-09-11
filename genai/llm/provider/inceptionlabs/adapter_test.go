package inceptionlabs

import (
	"encoding/json"
	"testing"

	"github.com/viant/agently-core/genai/llm"
)

func TestToRequest_MercuryCurrentContract(t *testing.T) {
	request, err := ToRequest(&llm.GenerateRequest{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "check 168 / 7 = 24"}},
		Options: &llm.Options{
			Model:     "mercury-2",
			MaxTokens: 128,
			Reasoning: &llm.Reasoning{Effort: "instant"},
			OutputSchema: map[string]interface{}{
				"type": "object",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]interface{}
	if err = json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["max_tokens"] != float64(128) {
		t.Fatalf("max_tokens = %#v", payload["max_tokens"])
	}
	if _, ok := payload["max_completion_tokens"]; ok {
		t.Fatal("legacy max_completion_tokens must not be sent")
	}
	if payload["reasoning_effort"] != "instant" {
		t.Fatalf("reasoning_effort = %#v", payload["reasoning_effort"])
	}
	format, ok := payload["response_format"].(map[string]interface{})
	if !ok || format["type"] != "json_schema" {
		t.Fatalf("response_format = %#v", payload["response_format"])
	}
}

func TestToRequest_JSONMode(t *testing.T) {
	request, err := ToRequest(&llm.GenerateRequest{
		Options: &llm.Options{Model: "mercury-2", JSONMode: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if request.ResponseFormat == nil || request.ResponseFormat.Type != "json_object" {
		t.Fatalf("response format = %#v", request.ResponseFormat)
	}
}

func TestToLLMSResponse_MercuryUsage(t *testing.T) {
	response := ToLLMSResponse(&Response{Usage: Usage{
		PromptTokens:      100,
		CompletionTokens:  20,
		TotalTokens:       127,
		CachedInputTokens: 90,
		ReasoningTokens:   7,
	}})
	if response.Usage.PromptCachedTokens != 90 {
		t.Fatalf("cached tokens = %d", response.Usage.PromptCachedTokens)
	}
	if response.Usage.ReasoningTokens != 7 {
		t.Fatalf("reasoning tokens = %d", response.Usage.ReasoningTokens)
	}
}
