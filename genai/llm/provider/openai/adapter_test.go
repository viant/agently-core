package openai

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/viant/agently-core/genai/llm"
	basecfg "github.com/viant/agently-core/genai/llm/provider/base"
)

func messageTextContent(content interface{}) string {
	switch actual := content.(type) {
	case string:
		return actual
	case []ContentItem:
		var parts []string
		for _, item := range actual {
			parts = append(parts, item.Text)
		}
		return strings.Join(parts, "\n")
	default:
		return ""
	}
}

func TestToRequest_StreamFlag(t *testing.T) {
	testCases := []struct {
		description string
		input       llm.GenerateRequest
		expected    *Request
	}{
		{
			description: "streaming enabled",
			input: llm.GenerateRequest{
				Messages: []llm.Message{
					llm.NewUserMessage("Hello from user"),
				},
				Options: &llm.Options{
					Model:  "test-model",
					Stream: true,
				},
			},
			expected: &Request{
				Model:  "test-model",
				Stream: true,
				Messages: []Message{
					{
						Role:    "user",
						Content: []ContentItem{{Type: "text", Text: "Hello from user"}},
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			actual := ToRequest(&tc.input)
			assert.EqualValues(t, tc.expected, actual)
		})
	}
}

// Test mapping of tool calls and tool call result ID from OpenAI response to llm.Message
func TestToLLMSResponse_ToolCallsAndToolCallId(t *testing.T) {
	// prepare a simulated OpenAI response with tool_calls and tool_call_id
	resp := &Response{
		ID:    "chatcmpl_123",
		Model: "gpt-test",
		Choices: []Choice{{
			Index: 0,
			Message: Message{
				Role:    "assistant",
				Name:    "assistant-name",
				Content: "result text",
				ToolCalls: []ToolCall{{
					ID:   "cid123",
					Type: "function",
					Function: FunctionCall{
						Name:      "doThing",
						Arguments: `{"x":1}`,
					},
				}},
				ToolCallId: "cid123",
			},
			FinishReason: "stop",
		}},
		Usage: Usage{PromptTokens: 2, CompletionTokens: 3, TotalTokens: 5},
	}
	out := ToLLMSResponse(resp)
	assert.Equal(t, "gpt-test", out.Model)
	assert.Equal(t, "chatcmpl_123", out.ResponseID)
	assert.Len(t, out.Choices, 1)
	msg := out.Choices[0].Message
	assert.EqualValues(t, llm.RoleAssistant, msg.Role)
	assert.Equal(t, "assistant-name", msg.Name)
	assert.Equal(t, "result text", msg.Content)
	// verify tool calls mapping
	expectedCalls := []llm.ToolCall{{
		ID:   "cid123",
		Type: "function",
		Function: llm.FunctionCall{
			Name:      "doThing",
			Arguments: `{"x":1}`,
		},
	}}
	assert.EqualValues(t, expectedCalls, msg.ToolCalls)
	// verify tool call result ID mapping
	assert.Equal(t, "cid123", msg.ToolCallId)
}

// TestToRequest_ReasoningSummary ensures that reasoning.summary="auto" is propagated
// only for supported models (o3, o4-mini, codex-mini-latest).
func TestToRequest_ReasoningSummary(t *testing.T) {
	testCases := []struct {
		description string
		input       llm.GenerateRequest
		expected    *Request
	}{
		{
			description: "reasoning summary auto for supported model",
			input: llm.GenerateRequest{
				Messages: []llm.Message{llm.NewUserMessage("Hello reasoning")},
				Options:  &llm.Options{Model: "o3", Reasoning: &llm.Reasoning{Summary: "auto"}},
			},
			expected: &Request{
				Model:     "o3",
				Reasoning: &llm.Reasoning{Summary: "auto"},
				Messages:  []Message{{Role: "user", Content: []ContentItem{{Type: "text", Text: "Hello reasoning"}}}},
			},
		},
		{
			description: "reasoning summary ignored for unsupported model",
			input: llm.GenerateRequest{
				Messages: []llm.Message{llm.NewUserMessage("Ignore")},
				Options:  &llm.Options{Model: "test-model", Reasoning: &llm.Reasoning{Summary: "auto"}},
			},
			expected: &Request{
				Model:    "test-model",
				Messages: []Message{{Role: "user", Content: []ContentItem{{Type: "text", Text: "Ignore"}}}},
			},
		},
		{
			description: "reasoning effort propagates for gpt-5-mini",
			input: llm.GenerateRequest{
				Messages: []llm.Message{llm.NewUserMessage("Classify")},
				Options:  &llm.Options{Model: "gpt-5-mini", Reasoning: &llm.Reasoning{Effort: "low"}},
			},
			expected: &Request{
				Model:     "gpt-5-mini",
				Reasoning: &llm.Reasoning{Effort: "low"},
				Messages:  []Message{{Role: "user", Content: []ContentItem{{Type: "text", Text: "Classify"}}}},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			actual := ToRequest(&tc.input)
			assert.EqualValues(t, tc.expected, actual)
		})
	}
}

func TestToRequest_ParallelToolCallsRequiresTools(t *testing.T) {
	t.Run("parallel tool calls omitted when no tools", func(t *testing.T) {
		in := llm.GenerateRequest{
			Messages: []llm.Message{llm.NewUserMessage("summarize")},
			Options: &llm.Options{
				Model:             "gpt-5.2",
				ParallelToolCalls: true,
			},
		}
		got := ToRequest(&in)
		assert.False(t, got.ParallelToolCalls)
		assert.Len(t, got.Tools, 0)
	})

	t.Run("parallel tool calls enabled when tools exist", func(t *testing.T) {
		in := llm.GenerateRequest{
			Messages: []llm.Message{llm.NewUserMessage("run tool")},
			Options: &llm.Options{
				Model:             "gpt-5.2",
				ParallelToolCalls: true,
				Tools: []llm.Tool{{
					Definition: llm.ToolDefinition{
						Name: "system_exec-execute",
						Parameters: map[string]interface{}{
							"type": "object",
						},
					},
				}},
			},
		}
		got := ToRequest(&in)
		assert.True(t, got.ParallelToolCalls)
		assert.Len(t, got.Tools, 1)
	})
}

func TestToRequest_ModelArtifactGenerationEnablesCodeInterpreter(t *testing.T) {
	in := llm.GenerateRequest{
		Messages: []llm.Message{llm.NewUserMessage("generate a pdf")},
		Options: &llm.Options{
			Model: "gpt-5.2",
			Metadata: map[string]interface{}{
				"modelArtifactGeneration": true,
			},
		},
	}

	got := ToRequest(&in)
	assert.Equal(t, "gpt-5.2", got.Model)
	assert.True(t, got.EnableCodeInterpreter)
	assert.EqualValues(t, map[string]interface{}{"type": "code_interpreter"}, got.ToolChoice)
}

func TestToRequest_SkipsTemperatureForUnsupportedGPT5Models(t *testing.T) {
	in := llm.GenerateRequest{
		Messages: []llm.Message{llm.NewUserMessage("analyze this repo")},
		Options: &llm.Options{
			Model:       "gpt-5-mini",
			Temperature: 0.2,
		},
	}

	got := ToRequest(&in)
	assert.Equal(t, "gpt-5-mini", got.Model)
	assert.Nil(t, got.Temperature)
}

func TestClientToRequest_SkipsTemperatureForClientDefaultGPT5Mini(t *testing.T) {
	client := &Client{Config: basecfg.Config{Model: "gpt-5-mini"}}
	in := &llm.GenerateRequest{
		Messages: []llm.Message{llm.NewUserMessage("analyze this repo")},
		Options: &llm.Options{
			Temperature: 0.2,
		},
	}

	got, err := client.ToRequest(in)
	assert.NoError(t, err)
	assert.Equal(t, "gpt-5-mini", got.Model)
	assert.Nil(t, got.Temperature)
}

func TestClientToRequest_BinaryInlineAndUploadValidation(t *testing.T) {
	const minimalPDFBase64 = "JVBERi0xLjQKMSAwIG9iago8PCAvVHlwZSAvQ2F0YWxvZyAvUGFnZXMgMiAwIFIgPj4KZW5kb2JqCjIgMCBvYmoKPDwgL1R5cGUgL1BhZ2VzIC9LaWRzIFszIDAgUl0gL0NvdW50IDEgPj4KZW5kb2JqCjMgMCBvYmoKPDwgL1R5cGUgL1BhZ2UgL1BhcmVudCAyIDAgUiAvTWVkaWFCb3ggWzAgMCAzMDAgMTQ0XSAvQ29udGVudHMgNCAwIFIgL1Jlc291cmNlcyA8PCAvRm9udCA8PCAvRjEgNSAwIFIgPj4gPj4gPj4KZW5kb2JqCjQgMCBvYmoKPDwgL0xlbmd0aCAzNyA+PgpzdHJlYW0KQlQgL0YxIDI0IFRmIDcyIDcyIFRkIChIZWxsbyBQREYpIFRqIEVUCmVuZHN0cmVhbQplbmRvYmoKNSAwIG9iago8PCAvVHlwZSAvRm9udCAvU3VidHlwZSAvVHlwZTEgL0Jhc2VGb250IC9IZWx2ZXRpY2EgPj4KZW5kb2JqCnhyZWYKMCA2CjAwMDAwMDAwMDAgNjU1MzUgZiAKMDAwMDAwMDAwOSAwMDAwMCBuIAowMDAwMDAwMDU4IDAwMDAwIG4gCjAwMDAwMDAxMTUgMDAwMDAgbiAKMDAwMDAwMDI0MSAwMDAwMCBuIAowMDAwMDAwMzMwIDAwMDAwIG4gCnRyYWlsZXIKPDwgL1Jvb3QgMSAwIFIgL1NpemUgNiA+PgpzdGFydHhyZWYKNDAwCiUlRU9GCg=="
	newReq := func(attachMode, mime string) *llm.GenerateRequest {
		data := "AA=="
		if mime == "application/pdf" {
			data = minimalPDFBase64
		}
		return &llm.GenerateRequest{
			Messages: []llm.Message{
				{
					Role: llm.RoleUser,
					Items: []llm.ContentItem{
						{
							Type:     llm.ContentTypeBinary,
							MimeType: mime,
							Data:     data,
							Name:     "sample.bin",
						},
					},
				},
			},
			Options: &llm.Options{
				Model: "gpt-4o-mini",
				Metadata: map[string]interface{}{
					"attachMode": attachMode,
				},
			},
		}
	}

	client := &Client{}

	t.Run("inline supports image", func(t *testing.T) {
		got, err := client.ToRequest(newReq("inline", "image/png"))
		assert.NoError(t, err)
		assert.Len(t, got.Messages, 1)
		items, ok := got.Messages[0].Content.([]ContentItem)
		if assert.True(t, ok) && assert.Len(t, items, 1) {
			assert.Equal(t, "image_url", items[0].Type)
			if assert.NotNil(t, items[0].ImageURL) {
				assert.Contains(t, items[0].ImageURL.URL, "data:image/png;base64,")
			}
		}
	})

	t.Run("inline supports pdf", func(t *testing.T) {
		got, err := client.ToRequest(newReq("inline", "application/pdf"))
		assert.NoError(t, err)
		assert.Len(t, got.Messages, 1)
		items, ok := got.Messages[0].Content.([]ContentItem)
		if assert.True(t, ok) && assert.Len(t, items, 1) {
			assert.Equal(t, "input_text", items[0].Type)
			assert.Contains(t, items[0].Text, "PDF attachment sample.bin:")
			assert.Contains(t, items[0].Text, "Hello PDF")
		}
	})

	t.Run("inline rejects unsupported mime", func(t *testing.T) {
		_, err := client.ToRequest(newReq("inline", "application/octet-stream"))
		if assert.Error(t, err) {
			assert.Contains(t, err.Error(), "unsupported inline binary content item mime type")
		}
	})

	t.Run("upload supports image", func(t *testing.T) {
		got, err := client.ToRequest(newReq("upload", "image/jpeg"))
		assert.NoError(t, err)
		assert.Len(t, got.Messages, 1)
		items, ok := got.Messages[0].Content.([]ContentItem)
		if assert.True(t, ok) && assert.Len(t, items, 1) {
			assert.Equal(t, "image_url", items[0].Type)
			if assert.NotNil(t, items[0].ImageURL) {
				assert.Contains(t, items[0].ImageURL.URL, "data:image/jpeg;base64,")
			}
		}
	})

	t.Run("upload rejects unsupported mime", func(t *testing.T) {
		_, err := client.ToRequest(newReq("upload", "text/plain"))
		if assert.Error(t, err) {
			assert.Contains(t, err.Error(), "unsupported uploaded binary content item mime type")
		}
	})
}

func TestToRequest_DoesNotRewriteLargeToolResultReplay(t *testing.T) {
	large := strings.Repeat("CHUNK-0000 LARGE_RESULT_SENTINEL\n", 512)
	in := llm.GenerateRequest{
		Messages: []llm.Message{
			llm.NewUserMessage("probe"),
			{
				Role: llm.RoleAssistant,
				ToolCalls: []llm.ToolCall{{
					ID:              "call_1",
					Name:            "mcplarge-large_result",
					ResultMessageID: "msg_tool_1",
					Type:            "function",
					Function: llm.FunctionCall{
						Name:      "mcplarge-large_result",
						Arguments: `{}`,
					},
				}},
			},
			{
				Role:       llm.RoleTool,
				Name:       "mcplarge-large_result",
				ToolCallId: "call_1",
				Content:    large,
			},
		},
		Options: &llm.Options{
			Model: "gpt-4o-mini",
			Metadata: map[string]interface{}{
				"toolResultPreviewLimit": 256,
			},
		},
	}

	got := ToRequest(&in)
	if assert.Len(t, got.Messages, 3) {
		text := messageTextContent(got.Messages[2].Content)
		assert.NotContains(t, text, "overflow: true")
		assert.NotContains(t, text, "useToolToSeeMore: message-show")
		assert.Contains(t, text, strings.Repeat("CHUNK-0000 LARGE_RESULT_SENTINEL\n", 20))
	}
}

func TestToRequest_PreservesNativeContinuationShape(t *testing.T) {
	large := strings.Repeat("A", 400) + "MIDPOINT" + strings.Repeat("Z", 400)
	body := `{"body":"` + large + `","continuation":{"hasMore":true,"remaining":4096,"returned":512,"nextRange":{"bytes":{"offset":512,"length":512}}}}`
	in := llm.GenerateRequest{
		Messages: []llm.Message{
			{
				Role:       llm.RoleTool,
				ToolCallId: "call_1",
				Content:    body,
			},
		},
		Options: &llm.Options{
			Model: "gpt-4o-mini",
			Metadata: map[string]interface{}{
				"toolResultPreviewLimit": 256,
			},
		},
	}

	got := ToRequest(&in)
	if assert.Len(t, got.Messages, 1) {
		text := messageTextContent(got.Messages[0].Content)
		assert.Contains(t, text, `"continuation":{"hasMore":true`)
		assert.Contains(t, text, `"nextRange":{"bytes":{"offset":512,"length":512}}`)
		assert.NotContains(t, text, "useToolToSeeMore: message-show")
		assert.NotContains(t, text, "[... omitted middle ...]")
	}
}
