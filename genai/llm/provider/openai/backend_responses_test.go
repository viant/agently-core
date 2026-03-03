package openai

import (
	"encoding/json"
	"strings"
	"testing"
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
