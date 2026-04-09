package openai

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildBackendWSURL(t *testing.T) {
	got, err := buildBackendWSURL("https://chatgpt.com/backend-api/codex")
	if err != nil {
		t.Fatalf("buildBackendWSURL error: %v", err)
	}
	want := "wss://chatgpt.com/backend-api/codex/responses"
	if got != want {
		t.Fatalf("unexpected ws url: got=%q want=%q", got, want)
	}
}

func TestBackendWSCreateRequest_DoesNotCarryProviderContinuationFields(t *testing.T) {
	req := backendWSCreateRequest{
		Type:         "response.create",
		Model:        "gpt-5.4",
		Instructions: "Use the transcript as provided.",
		Input: []InputItem{
			{Type: "message", Role: "user", Content: []ResponsesContentItem{{Type: "input_text", Text: "hello"}}},
			{Type: "function_call", CallID: "call_1", Name: "platform-tree", Arguments: `{"Field":"TargetingTree","Operation":"Get"}`},
			{Type: "function_call_output", CallID: "call_1", Output: `{"status":"error","message":"unsupported operation"}`},
		},
		Store:  false,
		Stream: true,
	}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	body := string(data)
	if strings.Contains(body, `"previous_response_id"`) {
		t.Fatalf("backend websocket request must not inject previous_response_id: %s", body)
	}
}
