package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
)

func TestResponsesPreservesExplicitReasoningAcrossModelVersions(t *testing.T) {
	for _, model := range []string{"gpt-6.1-sol", "future-model"} {
		for _, configured := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/explicit=%v", model, configured), func(t *testing.T) {
				server := newLocalServerOrSkip(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var body map[string]any
					require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
					require.Equal(t, "/responses", r.URL.Path)
					if configured {
						require.Equal(t, map[string]any{"effort": "low"}, body["reasoning"])
					} else {
						require.NotContains(t, body, "reasoning")
					}
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprint(w, `{"id":"fixture","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ready"}]}]}`)
				}))
				defer server.Close()
				client := NewClient("fixture-key", model, WithBaseURL(server.URL))
				client.HTTPClient = server.Client()
				request := &llm.GenerateRequest{Messages: []llm.Message{llm.NewUserMessage("question")}, Options: &llm.Options{Model: model}}
				if configured {
					request.Options.Reasoning = &llm.Reasoning{Effort: "low"}
				}
				_, err := client.Generate(context.Background(), request)
				require.NoError(t, err)
			})
		}
	}
}
