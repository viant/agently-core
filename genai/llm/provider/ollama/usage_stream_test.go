package ollama

import (
	"context"
	"fmt"
	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStreamPreservesFinalCumulativeUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, `{"model":"fixture","response":"hello","done":false}`)
		fmt.Fprintln(w, `{"model":"fixture","response":"","done":true,"prompt_eval_count":5,"eval_count":10}`)
	}))
	defer server.Close()
	client, err := NewClient(context.Background(), "fixture", WithBaseURL(server.URL))
	require.NoError(t, err)
	events, err := client.Stream(context.Background(), &llm.GenerateRequest{Messages: []llm.Message{llm.NewUserMessage("usage")}})
	require.NoError(t, err)
	var last *llm.Usage
	for event := range events {
		require.NoError(t, event.Err)
		if event.Usage != nil {
			last = event.Usage
		}
	}
	require.NotNil(t, last)
	require.Equal(t, 5, last.PromptTokens)
	require.Equal(t, 10, last.CompletionTokens)
	require.Equal(t, 15, last.TotalTokens)
}
