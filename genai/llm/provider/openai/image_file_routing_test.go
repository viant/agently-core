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

func imageFileRequest() *llm.GenerateRequest {
	return &llm.GenerateRequest{Instructions: "Keep original guidance", Messages: []llm.Message{{Role: llm.RoleUser, Items: []llm.ContentItem{{Type: llm.ContentTypeText, Text: "Read this image"}, {Type: llm.ContentTypeImage, Source: llm.SourceFile, Provider: "openai", Data: "file_image_exact", MimeType: "image/png"}}}}, Options: &llm.Options{MaxTokens: 128, Tools: []llm.Tool{{Type: "function", Definition: llm.ToolDefinition{Name: "lookup", Description: "Keep original tool"}}}}}
}

func TestUploadedImageRoutingPreservesProviderIDForCountGenerateAndStream(t *testing.T) {
	disabled := false
	for _, mode := range []string{"count", "generate", "stream", "upload"} {
		t.Run(mode, func(t *testing.T) {
			client := NewClient("fixture-key", "gpt-4o-mini", WithContextContinuation(&disabled))
			calls := 0
			client.HTTPClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				calls++
				require.Equal(t, "api.openai.com", request.URL.Host)
				require.Equal(t, "Bearer fixture-key", request.Header.Get("Authorization"))
				expected := "/v1/responses"
				if mode == "count" {
					expected += "/input_tokens"
				}
				require.Equal(t, expected, request.URL.Path)
				var payload map[string]any
				require.NoError(t, json.NewDecoder(request.Body).Decode(&payload))
				raw, _ := json.Marshal(payload)
				require.Contains(t, string(raw), `"type":"input_image"`)
				require.Contains(t, string(raw), `"file_id":"file_image_exact"`)
				require.Contains(t, string(raw), "Read this image")
				require.Contains(t, string(raw), "Keep original guidance")
				require.Contains(t, string(raw), "lookup")
				require.NotContains(t, string(raw), `"image_file"`)
				require.NotContains(t, payload, "previous_response_id")
				body := `{"object":"response","id":"resp_image","status":"completed","model":"gpt-4o-mini","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"red"}]}],"usage":{"input_tokens":10,"output_tokens":1,"total_tokens":11}}`
				headers := http.Header{"Content-Type": []string{"application/json"}}
				if mode == "count" {
					body = `{"input_tokens":10}`
				}
				if mode == "stream" {
					body = "data: {\"type\":\"response.completed\",\"response\":" + body + "}\n\ndata: [DONE]\n\n"
					headers.Set("Content-Type", "text/event-stream")
				}
				return &http.Response{StatusCode: 200, Header: headers, Body: io.NopCloser(strings.NewReader(body))}, nil
			})}
			request := imageFileRequest()
			if mode == "upload" {
				manager := &fakeOpenAIAssetManager{fileID: "file_image_exact"}
				client.storageMgr = manager
				client.storageMgrAPIKey = "fixture-key"
				client.storageMgrBaseURL = client.BaseURL
				request.Messages[0].Items[1] = llm.ContentItem{Type: llm.ContentTypeBinary, Name: "image.png", MimeType: "image/png", Data: "AA=="}
				request.Options.Metadata = map[string]interface{}{"attachMode": "upload"}
			}
			switch mode {
			case "count":
				tokens, err := client.CountInputTokens(context.Background(), request)
				require.NoError(t, err)
				require.Equal(t, 10, tokens)
			case "generate", "upload":
				result, err := client.Generate(context.Background(), request)
				require.NoError(t, err)
				require.NotNil(t, result)
			case "stream":
				events, err := client.Stream(context.Background(), request)
				require.NoError(t, err)
				for event := range events {
					require.NoError(t, event.Err)
				}
			}
			require.Equal(t, 1, calls)
			if mode == "upload" {
				require.Equal(t, "AA==", request.Messages[0].Items[1].Data)
			} else {
				require.Equal(t, "file_image_exact", request.Messages[0].Items[1].Data)
			}
		})
	}
}

func TestUploadedImageDoesNotSilentlySwitchCustomChatEndpoints(t *testing.T) {
	disabled := false
	for _, endpoint := range []string{"https://chat-compatible.invalid/v1", "https://chatgpt.com/backend-api/codex"} {
		client := NewClient("fixture-key", "gpt-4o-mini", WithContextContinuation(&disabled), WithBaseURL(endpoint))
		calls := 0
		client.HTTPClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			calls++
			t.Fatal("unsupported input dispatched")
			return nil, nil
		})}
		_, err := client.Generate(context.Background(), imageFileRequest())
		require.ErrorContains(t, err, "Responses-capable endpoint")
		require.Zero(t, calls)
	}
	client := NewClient("fixture-key", "gpt-4o-mini", WithContextContinuation(&disabled))
	ordinary := &Request{Messages: []Message{{Role: "user", Content: []ContentItem{{Type: "image_url", ImageURL: &ImageURL{URL: "data:image/png;base64,AA=="}}}}}}
	require.False(t, shouldUseResponsesAPI(client, ordinary), "supported inline Chat image keeps its route")
	prepared := &Request{Messages: []Message{{Role: "user", Content: []ContentItem{{Type: "image_file", File: &File{FileID: "file_image_exact"}}}}}}
	custom := NewClient("fixture-key", "gpt-4o-mini", WithContextContinuation(&disabled), WithBaseURL("https://chat-compatible.invalid/v1"))
	_, err := custom.marshalRequestBody(prepared)
	require.ErrorContains(t, err, "Responses-capable endpoint")
}
