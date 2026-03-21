package openai

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/viant/agently-core/genai/llm"
	basecfg "github.com/viant/agently-core/genai/llm/provider/base"
)

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
	newReq := func(attachMode, mime string) *llm.GenerateRequest {
		return &llm.GenerateRequest{
			Messages: []llm.Message{
				{
					Role: llm.RoleUser,
					Items: []llm.ContentItem{
						{
							Type:     llm.ContentTypeBinary,
							MimeType: mime,
							Data:     "AA==",
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
			assert.Equal(t, "file", items[0].Type)
			if assert.NotNil(t, items[0].File) {
				assert.Contains(t, items[0].File.FileData, "data:application/pdf;base64,")
			}
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
