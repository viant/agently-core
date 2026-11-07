package openai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
)

func TestChatGeneratePreservesOutputSchema(t *testing.T) {
	continuation := false
	client := NewClient("synthetic", "example-model", WithContextContinuation(&continuation))
	client.BaseURL = "http://example.invalid"
	client.HTTPClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		require.Equal(t, "/chat/completions", req.URL.Path)
		var payload map[string]any
		require.NoError(t, json.NewDecoder(req.Body).Decode(&payload))
		format, ok := payload["response_format"].(map[string]any)
		require.True(t, ok, "output schema must reach the Chat Completions endpoint")
		require.Equal(t, "json_schema", format["type"])
		schema := format["json_schema"].(map[string]any)
		require.Equal(t, true, schema["strict"])
		require.Equal(t, "number", schema["schema"].(map[string]any)["properties"].(map[string]any)["confidence"].(map[string]any)["type"])
		require.NotContains(t, payload, "text")
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"role":"assistant","content":"{\"confidence\":0.9}"},"finish_reason":"stop"}]}`))}, nil
	})}
	_, err := client.Generate(context.Background(), &llm.GenerateRequest{Messages: []llm.Message{llm.NewUserMessage("Classify the request.")}, Options: &llm.Options{OutputSchema: map[string]any{"type": "object", "properties": map[string]any{"confidence": map[string]any{"type": "number"}}, "required": []string{"confidence"}, "additionalProperties": false}}})
	require.NoError(t, err)
}

func TestChatSchemaSerializationPreservesCallerAndOrdinaryRequests(t *testing.T) {
	client := NewClient("synthetic", "example-model")
	for _, stream := range []bool{false, true} {
		request := &Request{Model: "example-model", Stream: stream, Text: &TextControls{Format: &TextFormat{Type: "json_schema", Name: "selection", Strict: true, Schema: map[string]any{"type": "object"}}}}
		body, err := client.marshalChatCompletionApiRequestBody(request)
		require.NoError(t, err)
		var payload map[string]any
		require.NoError(t, json.Unmarshal(body, &payload))
		require.Contains(t, payload, "response_format")
		require.NotContains(t, payload, "text")
		require.NotNil(t, request.Text, "serializing Chat must not erase the caller's schema")
		request.Text = nil
		body, err = client.marshalChatCompletionApiRequestBody(request)
		require.NoError(t, err)
		payload = nil
		require.NoError(t, json.Unmarshal(body, &payload))
		require.NotContains(t, payload, "response_format", "formatting remains opt-in")
	}
}
