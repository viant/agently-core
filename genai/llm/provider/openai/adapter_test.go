package openai

import (
	"context"
	"errors"
	"io"
	"os"
	"path"
	"strings"
	"testing"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/viant/afs/file"
	"github.com/viant/afs/object"
	"github.com/viant/afs/option/content"
	"github.com/viant/afs/storage"
	"github.com/viant/afsc/openai/assets"
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

type fakeOpenAIAssetManager struct {
	uploadedName string
	uploadedBody []byte
	purpose      string
	fileID       string
}

func (m *fakeOpenAIAssetManager) List(context.Context, string, ...storage.Option) ([]storage.Object, error) {
	info := file.NewInfo(m.uploadedName, int64(len(m.uploadedBody)), 0644, time.Now(), false, assets.File{
		ID:       m.fileID,
		Filename: m.uploadedName,
		Purpose:  m.purpose,
	})
	return []storage.Object{object.New("openai://assets/"+m.uploadedName, info, nil)}, nil
}

func (m *fakeOpenAIAssetManager) Open(context.Context, storage.Object, ...storage.Option) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}

func (m *fakeOpenAIAssetManager) OpenURL(context.Context, string, ...storage.Option) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}

func (m *fakeOpenAIAssetManager) Upload(_ context.Context, URL string, _ os.FileMode, reader io.Reader, options ...storage.Option) error {
	body, err := io.ReadAll(reader)
	if err != nil {
		return err
	}
	m.uploadedName = path.Base(URL)
	m.uploadedBody = body
	for _, opt := range options {
		if meta, ok := opt.(*content.Meta); ok {
			m.purpose = meta.Values["purpose"]
		}
	}
	return nil
}

func (m *fakeOpenAIAssetManager) Delete(context.Context, string, ...storage.Option) error {
	return nil
}

func (m *fakeOpenAIAssetManager) Create(context.Context, string, os.FileMode, bool, ...storage.Option) error {
	return nil
}

func (m *fakeOpenAIAssetManager) Close() error {
	return nil
}

func (m *fakeOpenAIAssetManager) Scheme() string {
	return "openai"
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

func TestStrictJSONSchema_ClosesNestedObjectNodes(t *testing.T) {
	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"template": map[string]interface{}{"const": "TutorAction"},
			"variant": map[string]interface{}{
				"oneOf": []interface{}{
					map[string]interface{}{"type": "string"},
					map[string]interface{}{"type": "number"},
				},
			},
			"nested": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name": map[string]interface{}{"type": "string"},
				},
			},
			"entries": map[string]interface{}{
				"type": "array",
				"items": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"value": map[string]interface{}{"type": "string"},
					},
				},
			},
		},
	}

	actual := strictJSONSchema(schema)
	assert.Equal(t, false, actual["additionalProperties"])
	assert.Equal(t, []string{"entries", "nested", "template", "variant"}, actual["required"])
	template := actual["properties"].(map[string]interface{})["template"].(map[string]interface{})
	assert.Equal(t, "string", template["type"])
	variant := actual["properties"].(map[string]interface{})["variant"].(map[string]interface{})
	assert.NotContains(t, variant, "oneOf")
	assert.Len(t, variant["anyOf"].([]interface{}), 2)
	nested := actual["properties"].(map[string]interface{})["nested"].(map[string]interface{})
	assert.Equal(t, false, nested["additionalProperties"])
	assert.Equal(t, []string{"name"}, nested["required"])
	items := actual["properties"].(map[string]interface{})["entries"].(map[string]interface{})["items"].(map[string]interface{})
	assert.Equal(t, false, items["additionalProperties"])
	assert.Equal(t, []string{"value"}, items["required"])
	_, originalChanged := schema["additionalProperties"]
	assert.False(t, originalChanged)
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

func TestClientToRequest_StripsReasoningWhenContextContinuationDisabled(t *testing.T) {
	disabled := false
	client := &Client{ContextContinuation: &disabled}
	in := &llm.GenerateRequest{
		Messages: []llm.Message{llm.NewUserMessage("Classify")},
		Options:  &llm.Options{Model: "gpt-5.4", Reasoning: &llm.Reasoning{Effort: "low"}},
	}

	got, err := client.ToRequest(in)
	if err != nil {
		t.Fatalf("ToRequest failed: %v", err)
	}
	if got.Reasoning != nil {
		t.Fatalf("expected reasoning to be stripped when contextContinuation is disabled, got %#v", got.Reasoning)
	}
}

func TestToRequest_ParallelToolCallsRequiresTools(t *testing.T) {
	t.Run("parallel tool calls omitted when no tools", func(t *testing.T) {
		in := llm.GenerateRequest{
			Messages: []llm.Message{llm.NewUserMessage("summarize")},
			Options: &llm.Options{
				Model:             "gpt-5.2",
				ParallelToolCalls: llm.BoolPtr(true),
			},
		}
		got := ToRequest(&in)
		assert.Nil(t, got.ParallelToolCalls)
		assert.Len(t, got.Tools, 0)
	})

	t.Run("parallel tool calls enabled when tools exist", func(t *testing.T) {
		in := llm.GenerateRequest{
			Messages: []llm.Message{llm.NewUserMessage("run tool")},
			Options: &llm.Options{
				Model:             "gpt-5.2",
				ParallelToolCalls: llm.BoolPtr(true),
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
		require.NotNil(t, got.ParallelToolCalls)
		assert.True(t, *got.ParallelToolCalls)
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

func TestToRequest_ModelArtifactGenerationDoesNotOverrideFunctionTools(t *testing.T) {
	in := llm.GenerateRequest{
		Messages: []llm.Message{llm.NewUserMessage("forecast this audience")},
		Options: &llm.Options{
			Model: "gpt-5.2",
			Tools: []llm.Tool{{
				Definition: llm.ToolDefinition{
					Name: "workspace-ForecastCube",
					Parameters: map[string]interface{}{
						"type": "object",
					},
				},
			}},
			Metadata: map[string]interface{}{
				"modelArtifactGeneration": true,
			},
		},
	}

	got := ToRequest(&in)
	assert.Equal(t, "gpt-5.2", got.Model)
	assert.True(t, got.EnableCodeInterpreter)
	assert.Equal(t, "auto", got.ToolChoice)
	require.Len(t, got.Tools, 1)
	assert.Equal(t, "workspace-ForecastCube", got.Tools[0].Function.Name)
}

func TestToRequest_SkipsTemperatureForUnsupportedGPT5Models(t *testing.T) {
	in := llm.GenerateRequest{
		Messages: []llm.Message{llm.NewUserMessage("analyze this repo")},
		Options: &llm.Options{
			Model:       "gpt-5.6-luna",
			Temperature: 0.2,
		},
	}

	got := ToRequest(&in)
	assert.Equal(t, "gpt-5.6-luna", got.Model)
	assert.Nil(t, got.Temperature)
}

func TestClientToRequest_SkipsTemperatureForClientDefaultGPT5Models(t *testing.T) {
	temperature := 0.2
	client := &Client{Config: basecfg.Config{Model: "gpt-5.6-luna"}, Temperature: &temperature}
	in := &llm.GenerateRequest{
		Messages: []llm.Message{llm.NewUserMessage("analyze this repo")},
		Options: &llm.Options{
			Temperature: 0.2,
		},
	}

	got, err := client.prepareChatRequest(in)
	assert.NoError(t, err)
	assert.Equal(t, "gpt-5.6-luna", got.Model)
	assert.Nil(t, got.Temperature)
}

func TestClientToRequest_BinaryInlineAndUploadValidation(t *testing.T) {
	const minimalPDFBase64 = "JVBERi0xLjQKMSAwIG9iago8PCAvVHlwZSAvQ2F0YWxvZyAvUGFnZXMgMiAwIFIgPj4KZW5kb2JqCjIgMCBvYmoKPDwgL1R5cGUgL1BhZ2VzIC9LaWRzIFszIDAgUl0gL0NvdW50IDEgPj4KZW5kb2JqCjMgMCBvYmoKPDwgL1R5cGUgL1BhZ2UgL1BhcmVudCAyIDAgUiAvTWVkaWFCb3ggWzAgMCAzMDAgMTQ0XSAvQ29udGVudHMgNCAwIFIgL1Jlc291cmNlcyA8PCAvRm9udCA8PCAvRjEgNSAwIFIgPj4gPj4gPj4KZW5kb2JqCjQgMCBvYmoKPDwgL0xlbmd0aCAzNyA+PgpzdHJlYW0KQlQgL0YxIDI0IFRmIDcyIDcyIFRkIChIZWxsbyBQREYpIFRqIEVUCmVuZHN0cmVhbQplbmRvYmoKNSAwIG9iago8PCAvVHlwZSAvRm9udCAvU3VidHlwZSAvVHlwZTEgL0Jhc2VGb250IC9IZWx2ZXRpY2EgPj4KZW5kb2JqCnhyZWYKMCA2CjAwMDAwMDAwMDAgNjU1MzUgZiAKMDAwMDAwMDAwOSAwMDAwMCBuIAowMDAwMDAwMDU4IDAwMDAwIG4gCjAwMDAwMDAxMTUgMDAwMDAgbiAKMDAwMDAwMDI0MSAwMDAwMCBuIAowMDAwMDAwMzMwIDAwMDAwIG4gCnRyYWlsZXIKPDwgL1Jvb3QgMSAwIFIgL1NpemUgNiA+PgpzdGFydHhyZWYKNDAwCiUlRU9GCg=="
	newReq := func(attachMode, mime string) *llm.GenerateRequest {
		data := "AA=="
		if mime == "application/pdf" {
			data = minimalPDFBase64
		}
		name := "sample.bin"
		switch mime {
		case "application/pdf":
			name = "sample.pdf"
		case "text/plain":
			name = "sample.txt"
		case "text/csv":
			name = "sample.csv"
		case "application/vnd.openxmlformats-officedocument.wordprocessingml.document":
			name = "sample.docx"
		case "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":
			name = "sample.xlsx"
		case "image/avif":
			name = "sample.avif"
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
							Name:     name,
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

	t.Run("inline supports image by extension when mime is empty", func(t *testing.T) {
		req := newReq("inline", "")
		req.Messages[0].Items[0].Name = "sample.jpeg"
		got, err := client.ToRequest(req)
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

	t.Run("inline supports pdf", func(t *testing.T) {
		got, err := client.ToRequest(newReq("inline", "application/pdf"))
		assert.NoError(t, err)
		assert.Len(t, got.Messages, 1)
		items, ok := got.Messages[0].Content.([]ContentItem)
		if assert.True(t, ok) && assert.Len(t, items, 1) {
			assert.Equal(t, "file", items[0].Type)
			if assert.NotNil(t, items[0].File) {
				assert.Equal(t, "sample.pdf", items[0].File.FileName)
				assert.Contains(t, items[0].File.FileData, "data:application/pdf;base64,")
			}
		}

		responseItems := toResponsesContentItems(got.Messages[0].Content, false)
		if assert.Len(t, responseItems, 1) {
			assert.Equal(t, "input_file", responseItems[0].Type)
			assert.Equal(t, "sample.pdf", responseItems[0].FileName)
			assert.Contains(t, responseItems[0].FileData, "data:application/pdf;base64,")
		}
	})

	for _, tc := range []struct {
		name string
		mime string
	}{
		{name: "text", mime: "text/plain"},
		{name: "csv", mime: "text/csv"},
		{name: "docx", mime: "application/vnd.openxmlformats-officedocument.wordprocessingml.document"},
		{name: "xlsx", mime: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"},
	} {
		t.Run("inline supports "+tc.name, func(t *testing.T) {
			got, err := client.ToRequest(newReq("inline", tc.mime))
			assert.NoError(t, err)
			assert.Len(t, got.Messages, 1)
			items, ok := got.Messages[0].Content.([]ContentItem)
			if assert.True(t, ok) && assert.Len(t, items, 1) {
				assert.Equal(t, "file", items[0].Type)
				if assert.NotNil(t, items[0].File) {
					assert.Contains(t, items[0].File.FileData, "data:"+tc.mime+";base64,")
				}
			}

			responseItems := toResponsesContentItems(got.Messages[0].Content, false)
			if assert.Len(t, responseItems, 1) {
				assert.Equal(t, "input_file", responseItems[0].Type)
				assert.Contains(t, responseItems[0].FileData, "data:"+tc.mime+";base64,")
			}
		})
	}

	t.Run("inline rejects unsupported image mime", func(t *testing.T) {
		_, err := client.ToRequest(newReq("inline", "image/avif"))
		if assert.Error(t, err) {
			assert.Contains(t, err.Error(), "unsupported inline binary content item mime type")
			var capabilityErr *llm.AttachmentCapabilityError
			assert.True(t, errors.As(err, &capabilityErr))
			if assert.NotNil(t, capabilityErr) {
				assert.Equal(t, "image/avif", capabilityErr.MIMEType)
				assert.Equal(t, "inline", capabilityErr.Mode)
			}
		}
	})

	t.Run("inline rejects unsupported mime", func(t *testing.T) {
		_, err := client.ToRequest(newReq("inline", "application/octet-stream"))
		if assert.Error(t, err) {
			assert.Contains(t, err.Error(), "unsupported inline binary content item mime type")
			var capabilityErr *llm.AttachmentCapabilityError
			assert.True(t, errors.As(err, &capabilityErr))
			if assert.NotNil(t, capabilityErr) {
				assert.Equal(t, "application/octet-stream", capabilityErr.MIMEType)
				assert.Equal(t, "inline", capabilityErr.Mode)
			}
		}
	})

	t.Run("upload supports image", func(t *testing.T) {
		storageMgr := &fakeOpenAIAssetManager{fileID: "file_vision_123"}
		client := &Client{
			APIKey:           "test-key",
			storageMgrAPIKey: "test-key",
			storageMgr:       storageMgr,
		}
		got, err := client.ToRequest(newReq("upload", "image/jpeg"))
		assert.NoError(t, err)
		assert.Len(t, got.Messages, 1)
		items, ok := got.Messages[0].Content.([]ContentItem)
		if assert.True(t, ok) && assert.Len(t, items, 1) {
			assert.Equal(t, "image_file", items[0].Type)
			if assert.NotNil(t, items[0].File) {
				assert.Equal(t, "file_vision_123", items[0].File.FileID)
			}
		}
		assert.Equal(t, string(openai.FilePurposeVision), storageMgr.purpose)
		assert.Equal(t, []byte{0}, storageMgr.uploadedBody)

		responseItems := toResponsesContentItems(got.Messages[0].Content, false)
		if assert.Len(t, responseItems, 1) {
			assert.Equal(t, "input_image", responseItems[0].Type)
			assert.Equal(t, "file_vision_123", responseItems[0].FileID)
			assert.Empty(t, responseItems[0].ImageURL)
		}
	})

	t.Run("upload rejects unsupported mime", func(t *testing.T) {
		_, err := client.ToRequest(newReq("upload", "application/octet-stream"))
		if assert.Error(t, err) {
			assert.Contains(t, err.Error(), "unsupported uploaded binary content item mime type")
			var capabilityErr *llm.AttachmentCapabilityError
			assert.True(t, errors.As(err, &capabilityErr))
			if assert.NotNil(t, capabilityErr) {
				assert.Equal(t, "application/octet-stream", capabilityErr.MIMEType)
				assert.Equal(t, "upload", capabilityErr.Mode)
			}
		}
	})
}

func TestOpenAIInputFileSupport(t *testing.T) {
	cases := []struct {
		name string
		mime string
		want bool
	}{
		{name: "report.pdf", mime: "application/pdf", want: true},
		{name: "notes.txt", mime: "text/plain", want: true},
		{name: "data.json", mime: "application/json", want: true},
		{name: "sheet.xlsx", mime: "application/octet-stream", want: true},
		{name: "slides.pptx", mime: "", want: true},
		{name: "document.docx", mime: "", want: true},
		{name: "main.go", mime: "", want: true},
		{name: "photo.png", mime: "image/png", want: false},
		{name: "archive.zip", mime: "application/zip", want: false},
		{name: "blob.bin", mime: "application/octet-stream", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name+" "+tc.mime, func(t *testing.T) {
			assert.Equal(t, tc.want, isOpenAIInputFileSupported(tc.mime, tc.name))
		})
	}
}

func TestOpenAIInlineImageSupport(t *testing.T) {
	cases := []struct {
		name string
		mime string
		want bool
	}{
		{name: "photo.png", mime: "image/png", want: true},
		{name: "photo.jpg", mime: "image/jpeg", want: true},
		{name: "photo.jpeg", mime: "", want: true},
		{name: "photo.webp", mime: "image/webp", want: true},
		{name: "photo.gif", mime: "image/gif", want: true},
		{name: "photo.avif", mime: "image/avif", want: false},
		{name: "vector.svg", mime: "image/svg+xml", want: false},
		{name: "archive.zip", mime: "application/zip", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name+" "+tc.mime, func(t *testing.T) {
			assert.Equal(t, tc.want, isOpenAIInlineImageSupported(tc.mime, tc.name))
		})
	}
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

func TestToRequest_LargeJSONToolResultCarriesStructuredNextArgs(t *testing.T) {
	body := `{"messageId":"source-msg","content":"` + strings.Repeat("A", 5000) + `"}`
	msg := llm.Message{
		ID:         "source-msg",
		Role:       llm.RoleTool,
		ToolCallId: "call_1",
		Content:    body,
	}
	got := sanitizeToolReplayMessage(msg, 1000)
	assert.Contains(t, got.Content, `"messageId":"source-msg"`)
	assert.Contains(t, got.Content, `"nextArgs":{`)
	assert.Contains(t, got.Content, `"messageId":"source-msg"`)
	assert.Contains(t, got.Content, `"byteRange":{"from":900,"to":1800}`)
	assert.NotContains(t, got.Content, "useToolToSeeMore: message-show")
}
