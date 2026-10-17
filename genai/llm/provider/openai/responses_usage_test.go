package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
	mcbuf "github.com/viant/agently-core/service/core/modelcall"
)

func TestResponsesUsagePreservesReportedDetailsForStreamAndGenerate(t *testing.T) {
	cases := []struct {
		name, details     string
		cached, reasoning *int
	}{
		{name: "reported", details: `,"input_tokens_details":{"cached_tokens":43452,"cache_write_tokens":19},"output_tokens_details":{"reasoning_tokens":17}`, cached: usageCount(43452), reasoning: usageCount(17)},
		{name: "zero", details: `,"input_tokens_details":{"cached_tokens":0},"output_tokens_details":{"reasoning_tokens":0}`, cached: usageCount(0), reasoning: usageCount(0)},
		{name: "absent"},
		{name: "empty-details", details: `,"input_tokens_details":{},"output_tokens_details":{}`},
		{name: "null-details", details: `,"input_tokens_details":null,"output_tokens_details":null`},
		{name: "null-counts", details: `,"input_tokens_details":{"cached_tokens":null},"output_tokens_details":{"reasoning_tokens":null}`},
		{name: "only-cached", details: `,"input_tokens_details":{"cached_tokens":0}`, cached: usageCount(0)},
		{name: "only-reasoning", details: `,"output_tokens_details":{"reasoning_tokens":17}`, reasoning: usageCount(17)},
	}
	for _, tc := range cases {
		for _, stream := range []bool{false, true} {
			for _, wrapped := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/stream=%v/wrapped=%v", tc.name, stream, wrapped), func(t *testing.T) {
					response := fmt.Sprintf(`{"id":"resp_fixture","status":"completed","model":"fixture-model","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ready"}]}],"usage":{"input_tokens":50000,"output_tokens":100,"total_tokens":50100%s}}`, tc.details)
					if wrapped {
						response = `{"type":"response.completed","response":` + response + `}`
					}
					server := newLocalServerOrSkip(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						require.Equal(t, "/responses", r.URL.Path)
						if stream {
							w.Header().Set("Content-Type", "text/event-stream")
							fmt.Fprintf(w, "event: response.completed\ndata: %s\n\n", response)
						} else {
							w.Header().Set("Content-Type", "application/json")
							fmt.Fprint(w, response)
						}
					}))
					defer server.Close()
					client := NewClient("fixture-key", "fixture-model", WithBaseURL(server.URL))
					client.HTTPClient = server.Client()
					observer := &recordingObserver{}
					ctx, cancel := context.WithTimeout(mcbuf.WithObserver(context.Background(), observer), 3*time.Second)
					defer cancel()
					request := &llm.GenerateRequest{Messages: []llm.Message{llm.NewUserMessage("question")}}
					var result *llm.GenerateResponse
					if stream {
						events, err := client.Stream(ctx, request)
						require.NoError(t, err)
						for event := range events {
							require.NoError(t, event.Err)
							if event.Response != nil {
								result = event.Response
							}
						}
					} else {
						var err error
						result, err = client.Generate(ctx, request)
						require.NoError(t, err)
					}
					require.NotNil(t, result)
					assertResponsesUsage(t, result.Usage, tc.cached, tc.reasoning)
					require.Equal(t, 1, observer.endCount)
					require.Equal(t, result.Usage, observer.last.Usage)
					assertResponsesUsage(t, observer.last.LLMResponse.Usage, tc.cached, tc.reasoning)
				})
			}
		}
	}
}
func usageCount(value int) *int { return &value }
func assertResponsesUsage(t *testing.T, usage *llm.Usage, cached, reasoning *int) {
	t.Helper()
	require.NotNil(t, usage)
	require.Equal(t, 50000, usage.PromptTokens)
	require.Equal(t, 100, usage.CompletionTokens)
	require.Equal(t, 50100, usage.TotalTokens)
	if cached == nil {
		require.Nil(t, usage.PromptTokensDetails)
		require.Zero(t, usage.CachedTokens)
		require.Zero(t, usage.PromptCachedTokens)
	} else {
		require.NotNil(t, usage.PromptTokensDetails)
		require.Equal(t, cached, usage.PromptTokensDetails.CachedTokens)
		require.Equal(t, *cached, usage.CachedTokens)
		require.Equal(t, *cached, usage.PromptCachedTokens)
	}
	if reasoning == nil {
		require.Nil(t, usage.CompletionTokensDetails)
		require.Zero(t, usage.ReasoningTokens)
		require.Zero(t, usage.CompletionReasoningTokens)
	} else {
		require.NotNil(t, usage.CompletionTokensDetails)
		require.Equal(t, reasoning, usage.CompletionTokensDetails.ReasoningTokens)
		require.Equal(t, *reasoning, usage.ReasoningTokens)
		require.Equal(t, *reasoning, usage.CompletionReasoningTokens)
	}
	raw, err := json.Marshal(usage)
	require.NoError(t, err)
	var normalized map[string]interface{}
	require.NoError(t, json.Unmarshal(raw, &normalized))
	if cached == nil {
		require.NotContains(t, normalized, "prompt_tokens_details")
	} else {
		require.Equal(t, float64(*cached), normalized["prompt_tokens_details"].(map[string]interface{})["cached_tokens"])
	}
	if reasoning == nil {
		require.NotContains(t, normalized, "completion_tokens_details")
	} else {
		require.Equal(t, float64(*reasoning), normalized["completion_tokens_details"].(map[string]interface{})["reasoning_tokens"])
	}
	require.NotContains(t, string(raw), "cache_write_tokens")
}
