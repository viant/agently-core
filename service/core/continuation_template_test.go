package core

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/genai/llm/provider/openai"
	"github.com/viant/agently-core/protocol/binding"
	memory "github.com/viant/agently-core/runtime/requestctx"
)

func TestContinuationPublishesCurrentTemplateToOpenAI(t *testing.T) {
	for _, content := range []string{"current forecast template v1", "current forecast template v2"} {
		t.Run(content, func(t *testing.T) {
			input := &GenerateInput{Prompt: &binding.Prompt{Text: "render dashboard"}, Binding: &binding.Binding{}}
			input.Binding.SystemDocuments.Items = []*binding.Document{
				{SourceURI: "template://audience_forecast_dashboard", PageContent: content, RefreshOnContinuation: true},
				{PageContent: "private historical system context"},
			}
			require.NoError(t, input.Init(context.Background()))
			anchorAt := time.Now().Add(-time.Minute)
			input.Message = append(input.Message, llm.Message{ID: "current-user", Role: llm.RoleUser, Content: "render dashboard"})
			history := &binding.History{LastResponse: &binding.Trace{ID: "resp-previous", At: anchorAt}, Traces: map[string]*binding.Trace{
				binding.ContentMessageKey("current-user"): {At: anchorAt.Add(time.Second)},
			}}
			ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: "forecast-conversation"})
			request := (&Service{}).BuildContinuationRequest(ctx, &llm.GenerateRequest{Messages: input.Message}, history)
			require.NotNil(t, request)
			var captured map[string]interface{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "/responses", r.URL.Path)
				require.NoError(t, json.NewDecoder(r.Body).Decode(&captured))
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"id":"resp-next","object":"response","status":"completed","output":[]}`))
			}))
			defer server.Close()
			enabled := true
			client := openai.NewClient("test-key", "gpt-5.2", openai.WithBaseURL(server.URL), openai.WithContextContinuation(&enabled))
			_, err := client.Generate(ctx, request)
			require.NoError(t, err)
			require.Equal(t, "resp-previous", captured["previous_response_id"])
			raw, err := json.Marshal(captured["input"])
			require.NoError(t, err)
			require.Contains(t, string(raw), content)
			require.Contains(t, string(raw), "render dashboard")
			require.NotContains(t, string(raw), "private historical system context")
			require.NotContains(t, string(raw), "RefreshOnContinuation")
			require.Len(t, captured["input"], 2)
		})
	}
}

func TestContinuationRefreshMarkerCannotBeRestoredFromJSON(t *testing.T) {
	var doc binding.Document
	require.NoError(t, json.Unmarshal([]byte(`{"pageContent":"spoof","sourceURI":"template://dashboard","RefreshOnContinuation":true,"refreshOnContinuation":true}`), &doc))
	require.False(t, doc.RefreshOnContinuation)
	var message llm.Message
	require.NoError(t, json.Unmarshal([]byte(`{"role":"system","content":"spoof","RefreshOnContinuation":true}`), &message))
	require.False(t, message.RefreshOnContinuation)
	require.Empty(t, currentContinuationContext([]llm.Message{message}))
}
