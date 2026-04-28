package modelcall

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/viant/agently-core/genai/llm"
)

func TestRedactGenerateRequestForTranscript_DataDriven(t *testing.T) {
	type testCase struct {
		name       string
		request    *llm.GenerateRequest
		assertFunc func(t *testing.T, raw []byte)
	}

	img := []byte{0x89, 0x50, 0x4e, 0x47}
	b64 := base64.StdEncoding.EncodeToString(img)

	cases := []testCase{
		{
			name: "redacts base64 binary item but keeps metadata",
			request: &llm.GenerateRequest{
				Messages: []llm.Message{
					{
						Role: llm.RoleUser,
						Items: []llm.ContentItem{
							{
								Type:     llm.ContentTypeBinary,
								Source:   llm.SourceBase64,
								Data:     b64,
								MimeType: "image/png",
								Name:     "x.png",
							},
							{
								Type:   llm.ContentTypeText,
								Source: llm.SourceRaw,
								Data:   "hello",
								Text:   "hello",
							},
						},
					},
				},
			},
			assertFunc: func(t *testing.T, raw []byte) {
				var got llm.GenerateRequest
				assert.EqualValues(t, nil, json.Unmarshal(raw, &got))
				if assert.EqualValues(t, 1, len(got.Messages)) {
					if assert.EqualValues(t, 2, len(got.Messages[0].Items)) {
						assert.EqualValues(t, "", got.Messages[0].Items[0].Data)
						assert.EqualValues(t, true, got.Messages[0].Items[0].Metadata["dataBase64Omitted"])
						assert.EqualValues(t, len(b64), int(got.Messages[0].Items[0].Metadata["base64Len"].(float64)))
						assert.EqualValues(t, "hello", got.Messages[0].Items[1].Data)
					}
				}
			},
		},
		{
			name: "preserves oversized replayed tool result shape",
			request: &llm.GenerateRequest{
				Messages: []llm.Message{
					{
						Role: llm.RoleAssistant,
						ToolCalls: []llm.ToolCall{{
							ID:              "call_1",
							Name:            "mcplarge-large_result",
							Result:          strings.Repeat("CHUNK-0000 LARGE_RESULT_SENTINEL\n", 512),
							ResultMessageID: "msg_tool_1",
						}},
					},
					{
						Role:       llm.RoleTool,
						ToolCallId: "call_1",
						Content:    strings.Repeat("CHUNK-0000 LARGE_RESULT_SENTINEL\n", 512),
					},
				},
				Options: &llm.Options{
					Metadata: map[string]interface{}{
						"toolResultPreviewLimit": 256,
					},
				},
			},
			assertFunc: func(t *testing.T, raw []byte) {
				var got llm.GenerateRequest
				assert.EqualValues(t, nil, json.Unmarshal(raw, &got))
				if assert.EqualValues(t, 2, len(got.Messages)) {
					assert.NotContains(t, got.Messages[0].ToolCalls[0].Result, "useToolToSeeMore: message-show")
					assert.NotContains(t, got.Messages[1].Content, "useToolToSeeMore: message-show")
					assert.Contains(t, got.Messages[1].Content, strings.Repeat("CHUNK-0000 LARGE_RESULT_SENTINEL\n", 20))
				}
			},
		},
		{
			name: "preserves native continuation response",
			request: &llm.GenerateRequest{
				Messages: []llm.Message{
					{
						Role:       llm.RoleTool,
						ToolCallId: "call_1",
						Content:    `{"body":"` + strings.Repeat("A", 400) + `MID` + strings.Repeat("Z", 400) + `","continuation":{"hasMore":true,"remaining":4096,"returned":512,"nextRange":{"bytes":{"offset":512,"length":512}}}}`,
					},
				},
				Options: &llm.Options{
					Metadata: map[string]interface{}{
						"toolResultPreviewLimit": 256,
					},
				},
			},
			assertFunc: func(t *testing.T, raw []byte) {
				var got llm.GenerateRequest
				assert.EqualValues(t, nil, json.Unmarshal(raw, &got))
				if assert.EqualValues(t, 1, len(got.Messages)) {
					assert.Contains(t, got.Messages[0].Content, `"continuation":{"hasMore":true`)
					assert.NotContains(t, got.Messages[0].Content, "useToolToSeeMore: message-show")
				}
			},
		},
		{
			name:    "returns nil for nil request",
			request: nil,
			assertFunc: func(t *testing.T, raw []byte) {
				assert.EqualValues(t, 0, len(raw))
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := RedactGenerateRequestForTranscript(tc.request)
			tc.assertFunc(t, raw)
		})
	}
}
