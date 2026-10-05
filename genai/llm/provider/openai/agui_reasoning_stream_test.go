package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/protocol/agui"
	"github.com/viant/agently-core/runtime/streaming"
	"net/http"
	"strings"
	"testing"
)

func TestResponsesReasoningHTTPReachesStandardAGUIWithEncryptedArtifact(t *testing.T) {
	frames := []string{
		"event: response.created", `data: {"response":{"id":"response","model":"gpt-5.4","status":"in_progress"}}`,
		"event: response.reasoning_summary_text.delta", `data: {"item_id":"reason","delta":"Checking facts"}`,
		"event: response.output_item.done", `data: {"item":{"id":"reason","type":"reasoning","encrypted_content":"opaque-cipher"}}`,
		"event: response.output_text.delta", `data: {"item_id":"answer","delta":"answer"}`,
		"event: response.completed", `data: {"response":{"id":"response","model":"gpt-5.4","status":"completed","output":[{"id":"reason","type":"reasoning","encrypted_content":"opaque-cipher"},{"id":"answer","type":"message","role":"assistant","content":[{"type":"output_text","text":"answer"}]}],"usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5}}}`,
	}
	server := newLocalServerOrSkip(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, strings.Join(frames, "\n"))
	}))
	defer server.Close()
	client := &Client{APIKey: "test"}
	client.BaseURL = server.URL
	client.HTTPClient = server.Client()
	client.Model = "gpt-5.4"
	source, err := client.Stream(context.Background(), &llm.GenerateRequest{Messages: []llm.Message{llm.NewUserMessage("answer")}})
	require.NoError(t, err)
	translator := agui.NewTranslator("thread", "run")
	events := translator.Translate(&streaming.Event{Type: streaming.EventTypeModelStarted, ModelCallID: "model-call"})
	for item := range source {
		require.NoError(t, item.Err)
		if item.Kind == llm.StreamEventTurnCompleted || item.Kind == "" || item.Kind == llm.StreamEventTurnStarted {
			continue
		}
		native := streaming.FromLLMEvent("thread", item)
		events = append(events, translator.Translate(native)...)
	}
	events = append(events, translator.Translate(&streaming.Event{Type: streaming.EventTypeModelCompleted, ModelCallID: "model-call"})...)
	events = append(events, translator.Finish("success")...)
	var reasoning, text, cipher string
	for _, event := range events {
		raw, err := json.Marshal(event)
		require.NoError(t, err)
		require.NoError(t, agui.ValidateEvent(raw))
		var fields map[string]any
		require.NoError(t, json.Unmarshal(raw, &fields))
		switch event.Type {
		case "REASONING_MESSAGE_CONTENT":
			reasoning += fields["delta"].(string)
		case "TEXT_MESSAGE_CONTENT":
			text += fields["delta"].(string)
		case "REASONING_ENCRYPTED_VALUE":
			cipher = fields["encryptedValue"].(string)
		}
	}
	require.Equal(t, "Checking facts", reasoning)
	require.Equal(t, "answer", text)
	require.Equal(t, "opaque-cipher", cipher)
}
