package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/runtime/clienttool"
)

func TestCountInputTokens_ExactResponsesPayload(t *testing.T) {
	calls := 0
	var counted map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		require.Equal(t, "/v1/responses/input_tokens", r.URL.Path)
		require.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))
		require.NoError(t, json.NewDecoder(r.Body).Decode(&counted))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"input_tokens":9876}`))
	}))
	defer server.Close()
	client := NewClient("test-key", "gpt-5-nano", WithBaseURL(server.URL+"/v1"))
	call := llm.NewToolCall("call_1", "lookup", map[string]interface{}{"Unicode": "日本語 🧪", "escaped": "\"\\\n"}, "result 日本語")
	request := &llm.GenerateRequest{Instructions: "system instructions 🧪", Messages: []llm.Message{llm.NewTextMessage(llm.RoleUser, "facts 日本語"), llm.NewAssistantMessageWithToolCalls(call), llm.NewToolResultMessage(call)}, PromptCacheKey: "cache-fixture", Options: &llm.Options{MaxTokens: 8192, Stream: true, Tools: []llm.Tool{{Type: "function", Definition: llm.ToolDefinition{Name: "lookup"}}}}}
	media, err := clienttool.MapContent(json.RawMessage(`[{"type":"image","source":{"type":"url","value":"https://example.invalid/image.png","mimeType":"image/png"}},{"type":"document","source":{"type":"file","value":"file_test_opaque","provider":"openai","mimeType":"application/pdf"}}]`))
	request.Options.ToolChoice = llm.NewFunctionToolChoice("lookup")
	require.NoError(t, err)
	request.Messages = append(request.Messages, llm.Message{Role: llm.RoleUser, Items: media})
	tokens, err := client.CountInputTokens(context.Background(), request)
	require.NoError(t, err)
	require.Equal(t, 9876, tokens)
	require.Equal(t, 1, calls)
	prepared, err := client.prepareChatRequestContext(context.Background(), request)
	require.NoError(t, err)
	raw, err := client.marshalResponsesApiRequestBody(prepared)
	require.NoError(t, err)
	var generated map[string]interface{}
	require.NoError(t, json.Unmarshal(raw, &generated))
	for _, key := range []string{"max_output_tokens", "stream", "include", "prompt_cache_key", "temperature", "top_p", "n"} {
		delete(generated, key)
		require.NotContains(t, counted, key)
	}
	require.Equal(t, generated, counted)
	require.NotEmpty(t, counted["instructions"])
	require.NotEmpty(t, counted["tools"])
	require.NotEmpty(t, counted["input"])
	require.Equal(t, map[string]interface{}{"type": "function", "name": "lookup"}, counted["tool_choice"])
}

func TestCountInputTokens_ErrorsDoNotGenerate(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{{"unsupported", `{"error":"unsupported"}`, 404}, {"missing", `{}`, 200}, {"negative", `{"input_tokens":-1}`, 200}, {"invalid", `invalid`, 200}} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				require.Equal(t, "/responses/input_tokens", r.URL.Path)
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			client := NewClient("key", "gpt-5-nano", WithBaseURL(server.URL))
			_, err := client.CountInputTokens(context.Background(), &llm.GenerateRequest{})
			require.Error(t, err)
			require.Equal(t, 1, calls)
		})
	}
	client := NewClient("key", "gpt-5-nano", WithBaseURL("https://chatgpt.com/backend-api/codex"))
	_, err := client.CountInputTokens(context.Background(), &llm.GenerateRequest{})
	require.ErrorContains(t, err, "unavailable")
}
