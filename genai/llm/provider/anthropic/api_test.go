package anthropic

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
)

func TestClientGenerate_UsesAPIKeyHeader(t *testing.T) {
	var gotAPIKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAPIKey = r.Header.Get("x-api-key")
		require.Empty(t, r.Header.Get("Authorization"))
		require.Equal(t, "2023-06-01", r.Header.Get("anthropic-version"))
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"id":            "msg_1",
			"type":          "message",
			"role":          "assistant",
			"model":         "claude-sonnet-4-20250514",
			"content":       []map[string]any{{"type": "text", "text": "hello"}},
			"stop_reason":   "end_turn",
			"stop_sequence": nil,
			"usage":         map[string]any{"input_tokens": 3, "output_tokens": 2},
		}))
	}))
	defer srv.Close()

	client := NewClient("sk-ant-api-test", "claude-sonnet-4-20250514", WithBaseURL(srv.URL))
	out, err := client.Generate(context.Background(), &llm.GenerateRequest{
		Messages: []llm.Message{llm.NewUserMessage("hello")},
		Options:  &llm.Options{MaxTokens: 32},
	})
	require.NoError(t, err)
	require.Equal(t, "sk-ant-api-test", gotAPIKey)
	require.NotNil(t, out)
	require.Equal(t, "hello", out.Choices[0].Message.Content)
}

func TestClientGenerate_UsesBearerOAuthHeader(t *testing.T) {
	var gotAuth string
	var gotBeta string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotBeta = r.Header.Get("anthropic-beta")
		require.Empty(t, r.Header.Get("x-api-key"))
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"id":          "msg_2",
			"type":        "message",
			"role":        "assistant",
			"model":       "claude-sonnet-4-20250514",
			"content":     []map[string]any{{"type": "text", "text": "oauth"}},
			"stop_reason": "end_turn",
			"usage":       map[string]any{"input_tokens": 4, "output_tokens": 1},
		}))
	}))
	defer srv.Close()

	client := NewClient("", "claude-sonnet-4-20250514",
		WithBaseURL(srv.URL),
		WithAuthToken("oauth-token"),
	)
	out, err := client.Generate(context.Background(), &llm.GenerateRequest{
		Messages: []llm.Message{llm.NewUserMessage("hello")},
		Options:  &llm.Options{MaxTokens: 32},
	})
	require.NoError(t, err)
	require.Equal(t, "Bearer oauth-token", gotAuth)
	require.Equal(t, defaultOAuthBeta, gotBeta)
	require.Equal(t, "oauth", out.Choices[0].Message.Content)
}
