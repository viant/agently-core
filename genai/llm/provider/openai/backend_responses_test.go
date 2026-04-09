package openai

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/viant/agently-core/genai/llm"
)

func TestToChatGPTBackendResponsesPayload_Contract(t *testing.T) {
	temp := 0.7
	req := &Request{
		Model:       "gpt-5.2",
		Temperature: &temp,
		MaxTokens:   128,
		TopP:        0.9,
		N:           2,
		Messages: []Message{
			{Role: "system", Content: "Follow system rule A."},
			{Role: "user", Content: "hi"},
		},
	}

	payload := ToChatGPTBackendResponsesPayload(req)
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	body := string(raw)

	if !strings.Contains(body, "\"instructions\"") {
		t.Fatalf("expected instructions in payload: %s", body)
	}
	if strings.Contains(body, "\"temperature\"") {
		t.Fatalf("backend payload must not include temperature: %s", body)
	}
	if strings.Contains(body, "\"top_p\"") {
		t.Fatalf("backend payload must not include top_p: %s", body)
	}
	if strings.Contains(body, "\"n\"") {
		t.Fatalf("backend payload must not include n: %s", body)
	}
	if strings.Contains(body, "\"max_output_tokens\"") {
		t.Fatalf("backend payload must not include max_output_tokens: %s", body)
	}
	if strings.Contains(body, "\"previous_response_id\"") {
		t.Fatalf("backend payload must not include previous_response_id: %s", body)
	}
	if strings.Contains(body, "\"role\":\"system\"") {
		t.Fatalf("backend payload must not include role=system: %s", body)
	}
	if !payload.Stream {
		t.Fatalf("backend payload must force stream=true")
	}
	if payload.Store {
		t.Fatalf("backend payload must force store=false")
	}
}

func TestToChatGPTBackendResponsesPayload_PreservesExplicitInstructions(t *testing.T) {
	req := &Request{
		Model:        "gpt-5.2",
		Instructions: "Explicit instruction",
		Messages: []Message{
			{Role: "system", Content: "System guidance"},
			{Role: "user", Content: "hello"},
		},
	}

	payload := ToChatGPTBackendResponsesPayload(req)
	if payload.Instructions != "Explicit instruction" {
		t.Fatalf("expected explicit instructions to be preserved, got %q", payload.Instructions)
	}

	foundDeveloperSystem := false
	for _, item := range payload.Input {
		if strings.EqualFold(item.Type, "message") &&
			strings.EqualFold(item.Role, "developer") &&
			len(item.Content) > 0 &&
			strings.Contains(item.Content[0].Text, "System guidance") {
			foundDeveloperSystem = true
			break
		}
	}
	if !foundDeveloperSystem {
		t.Fatalf("expected transformed developer message with system guidance in input")
	}
}

func TestToResponsesPayload_PreservesToolHistoryWithoutPreviousResponseID(t *testing.T) {
	client := &Client{}
	req, err := client.ToRequest(&llm.GenerateRequest{
		Messages: []llm.Message{
			llm.NewSystemMessage("System guidance"),
			llm.NewUserMessage("what iris targeting do we have?"),
			{
				Role: llm.RoleAssistant,
				ToolCalls: []llm.ToolCall{{
					ID:   "call_1",
					Name: "platform-tree",
					Type: "function",
					Function: llm.FunctionCall{
						Name:      "platform-tree",
						Arguments: `{"Field":"TargetingTree","Operation":"Get"}`,
					},
				}},
			},
			{
				Role:       llm.RoleTool,
				Name:       "platform-tree",
				ToolCallId: "call_1",
				Content:    `{"status":"error","message":"unsupported operation"}`,
			},
		},
		Options: &llm.Options{Model: "gpt-5.4"},
	})
	if err != nil {
		t.Fatalf("ToRequest failed: %v", err)
	}

	payload := ToResponsesPayload(req)
	if len(payload.Input) != 4 {
		t.Fatalf("expected full transcript to map to 4 input items, got %d", len(payload.Input))
	}
	if payload.Input[2].Type != "function_call" {
		t.Fatalf("expected assistant tool request to map to function_call, got %#v", payload.Input[2])
	}
	if payload.Input[2].CallID != "call_1" || payload.Input[2].Name != "platform-tree" {
		t.Fatalf("unexpected function_call item: %#v", payload.Input[2])
	}
	if payload.Input[3].Type != "function_call_output" {
		t.Fatalf("expected tool result to map to function_call_output, got %#v", payload.Input[3])
	}
	if payload.Input[3].CallID != "call_1" || !strings.Contains(payload.Input[3].Output, "unsupported operation") {
		t.Fatalf("unexpected function_call_output item: %#v", payload.Input[3])
	}
}

func TestToChatGPTBackendResponsesPayload_PreservesToolHistoryWithoutProviderContinuation(t *testing.T) {
	client := &Client{}
	req, err := client.ToRequest(&llm.GenerateRequest{
		Messages: []llm.Message{
			llm.NewSystemMessage("System guidance"),
			llm.NewUserMessage("what iris targeting do we have?"),
			{
				Role: llm.RoleAssistant,
				ToolCalls: []llm.ToolCall{{
					ID:   "call_1",
					Name: "platform-tree",
					Type: "function",
					Function: llm.FunctionCall{
						Name:      "platform-tree",
						Arguments: `{"Field":"TargetingTree","Operation":"Get"}`,
					},
				}},
			},
			{
				Role:       llm.RoleTool,
				Name:       "platform-tree",
				ToolCallId: "call_1",
				Content:    `{"status":"error","message":"unsupported operation"}`,
			},
		},
		Options: &llm.Options{Model: "gpt-5.4"},
	})
	if err != nil {
		t.Fatalf("ToRequest failed: %v", err)
	}

	payload := ToChatGPTBackendResponsesPayload(req)
	if len(payload.Input) != 3 {
		t.Fatalf("expected backend payload to keep user + tool history, got %d items", len(payload.Input))
	}
	if payload.Input[1].Type != "function_call" || payload.Input[2].Type != "function_call_output" {
		t.Fatalf("expected backend payload to preserve function call/output history, got %#v", payload.Input)
	}
	if payload.Input[2].CallID != "call_1" || !strings.Contains(payload.Input[2].Output, "unsupported operation") {
		t.Fatalf("unexpected backend tool output item: %#v", payload.Input[2])
	}
}
