package clienttool_test

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/genai/llm/provider/anthropic"
	"github.com/viant/agently-core/genai/llm/provider/openai"
	"github.com/viant/agently-core/genai/llm/provider/vertexai/claude"
	"github.com/viant/agently-core/genai/llm/provider/vertexai/gemini"
	"github.com/viant/agently-core/protocol/agui"
	"github.com/viant/agently-core/runtime/clienttool"
	"testing"
)

func TestProtocolProviderMediaMatrix(t *testing.T) {
	for _, provider := range []string{"openai-responses", "openai-chat", "anthropic", "gemini", "vertex-claude"} {
		for _, kind := range []string{"image", "audio", "video", "document"} {
			for _, source := range []string{"url", "data", "file"} {
				for _, role := range []llm.MessageRole{llm.RoleUser, llm.RoleTool} {
					t.Run(fmt.Sprintf("%s/%s/%s/%s", provider, kind, source, role), func(t *testing.T) {
						mime := map[string]string{"image": "image/png", "audio": "audio/wav", "video": "video/mp4", "document": "application/pdf"}[kind]
						value := map[string]string{"url": "https://example.invalid/media", "data": "AQID", "file": "opaque-file-id"}[source]
						owner := map[string]string{"openai-responses": "openai", "openai-chat": "openai", "anthropic": "anthropic", "gemini": "google", "vertex-claude": "anthropic"}[provider]
						partSource := map[string]any{"type": source, "value": value, "mimeType": mime}
						if source == "file" {
							partSource["provider"] = owner
						}
						raw, _ := json.Marshal([]any{map[string]any{"type": "text", "text": " before\n"}, map[string]any{"type": kind, "source": partSource}, map[string]any{"type": "text", "text": " after\t"}})
						wireMessage := map[string]any{"id": "message", "role": role, "content": json.RawMessage(raw)}
						if role == llm.RoleTool {
							wireMessage["toolCallId"] = "call"
						}
						envelope, _ := json.Marshal(map[string]any{"threadId": "thread", "runId": "run", "messages": []any{wireMessage}})
						require.NoError(t, agui.ValidateInput(envelope))
						items, err := clienttool.MapContent(raw)
						require.NoError(t, err)
						message := llm.Message{Role: role, Items: items}
						if role == llm.RoleTool {
							message.ToolCallId = "call"
							message.Name = "lookup"
						}
						input := &llm.GenerateRequest{Messages: []llm.Message{message}}
						var parts []map[string]any
						supported := false
						switch provider {
						case "openai-responses", "openai-chat":
							responses := provider == "openai-responses"
							client := openai.NewClient("fixture", "gpt-fixture", openai.WithContextContinuation(&responses))
							supported = (kind == "image" && (responses || source != "file") || kind == "document" && (responses || source != "url") || kind == "audio" && !responses && source == "data") && (role != llm.RoleTool || responses)
							request, e := client.ToRequest(input)
							err = e
							if e == nil {
								if responses {
									wire := openai.ToResponsesPayload(request)
									if role == llm.RoleTool {
										parts = objects(t, wire.Input[0].OutputParts)
									} else {
										parts = objects(t, wire.Input[0].Content)
									}
								} else {
									parts = objects(t, request.Messages[0].Content)
								}
							}
						case "anthropic", "vertex-claude":
							supported = (kind == "image" || kind == "document") && (provider == "anthropic" || source == "data")
							if provider == "anthropic" {
								request, e := anthropic.ToRequest(context.Background(), "fixture", input)
								err = e
								if err == nil {
									if role == llm.RoleTool {
										parts = objects(t, request.Messages[0].Content[0].Content)
									} else {
										parts = objects(t, request.Messages[0].Content)
									}
								}
							} else {
								request, e := claude.ToRequest(context.Background(), input)
								err = e
								if err == nil {
									if role == llm.RoleTool {
										parts = objects(t, request.Messages[0].Content[0].Content)
									} else {
										parts = objects(t, request.Messages[0].Content)
									}
								}
							}
						case "gemini":
							supported = true
							request, e := gemini.ToRequest(context.Background(), input)
							err = e
							if e == nil {
								parts = objects(t, request.Contents[0].Parts)
								if role == llm.RoleTool {
									require.Contains(t, parts[0], "functionResponse")
									parts = parts[1:]
								}
							}
						}
						if !supported {
							require.Error(t, err, "unsupported media must fail explicitly before transport")
							return
						}
						require.NoError(t, err)
						require.Len(t, parts, 3, "media must not disappear or flatten")
						require.Equal(t, " before\n", parts[0]["text"])
						require.Equal(t, " after\t", parts[2]["text"])
						encoded, _ := json.Marshal(parts[1])
						require.Contains(t, string(encoded), value, "source value must survive mapping")
						if source == "data" && provider == "gemini" {
							require.Contains(t, parts[1], "inline_data")
						}
						if (source == "url" || source == "file") && provider == "gemini" {
							require.Contains(t, parts[1], "file_data")
						}
						if provider == "anthropic" || provider == "vertex-claude" {
							require.Equal(t, kind, parts[1]["type"])
							require.Contains(t, parts[1], "source")
						}
					})
				}
			}
		}
	}
}
func objects(t *testing.T, value any) []map[string]any {
	t.Helper()
	raw, err := json.Marshal(value)
	require.NoError(t, err)
	var parts []map[string]any
	require.NoError(t, json.Unmarshal(raw, &parts))
	return parts
}

func TestProtocolPlainTextDocumentMappingsAndUnsupportedMIME(t *testing.T) {
	raw := json.RawMessage(`[{"type":"document","source":{"type":"data","mimeType":"text/plain","value":"ZG9jdW1lbnQgdGV4dA=="}}]`)
	items, err := clienttool.MapContent(raw)
	require.NoError(t, err)
	for _, role := range []llm.MessageRole{llm.RoleUser, llm.RoleTool} {
		input := &llm.GenerateRequest{Messages: []llm.Message{{Role: role, Items: items}}}
		if role == llm.RoleTool {
			input.Messages[0].ToolCallId = "call"
		}
		for _, direct := range []bool{false, true} {
			ctx := context.Background()
			if direct {
				ctx = claude.WithDirectAPI(ctx)
			}
			request, err := claude.ToRequest(ctx, input)
			require.NoError(t, err)
			block := request.Messages[0].Content[0]
			if role == llm.RoleTool {
				block = block.Content.([]claude.ContentBlock)[0]
			}
			require.Equal(t, "document", block.Type)
			require.Equal(t, "text", block.Source.Type)
			require.Equal(t, "text/plain", block.Source.MediaType)
			require.Equal(t, "document text", block.Source.Data)
		}
		responses := true
		client := openai.NewClient("fixture", "fixture", openai.WithContextContinuation(&responses))
		request, err := client.ToRequest(input)
		require.NoError(t, err)
		payload := openai.ToResponsesPayload(request)
		parts := payload.Input[0].Content
		if role == llm.RoleTool {
			parts = payload.Input[0].OutputParts
		}
		require.Len(t, parts, 1)
		require.Equal(t, "input_file", parts[0].Type)
		require.Equal(t, "data:text/plain;base64,ZG9jdW1lbnQgdGV4dA==", parts[0].FileData)
	}
	for _, mime := range []string{"application/x-unsupported", "application/octet-stream"} {
		items[0].MimeType = mime
		input := &llm.GenerateRequest{Messages: []llm.Message{{Role: llm.RoleUser, Items: items}}}
		_, err := claude.ToRequest(context.Background(), input)
		require.ErrorContains(t, err, "inline documents require")
		_, err = openai.NewClient("fixture", "fixture").ToRequest(input)
		require.ErrorContains(t, err, "inline document MIME")
	}
	items[0].MimeType = "text/plain"
	items[0].Data = "/w=="
	_, err = claude.ToRequest(context.Background(), &llm.GenerateRequest{Messages: []llm.Message{{Role: llm.RoleUser, Items: items}}})
	require.ErrorContains(t, err, "valid UTF-8")
}
