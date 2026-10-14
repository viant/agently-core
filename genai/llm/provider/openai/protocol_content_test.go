package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/runtime/clienttool"
	"testing"
)

func TestProtocolContentFileIDsAndMultipartToolOutput(t *testing.T) {
	client := &Client{}
	client.Model = "gpt-5.2"
	items, err := clienttool.MapContent(json.RawMessage(`[{"type":"text","text":"first"},{"type":"document","source":{"type":"file","value":"file_opaque","provider":"openai","mimeType":"application/pdf"}},{"type":"text","text":"last"}]`))
	require.NoError(t, err)
	request, err := client.ToRequest(&llm.GenerateRequest{Messages: []llm.Message{{Role: llm.RoleTool, ToolCallId: "call", Items: items}}})
	require.NoError(t, err)
	payload := ToResponsesPayload(request)
	require.Len(t, payload.Input, 1)
	require.Equal(t, "function_call_output", payload.Input[0].Type)
	require.Len(t, payload.Input[0].OutputParts, 3)
	require.Equal(t, "first", payload.Input[0].OutputParts[0].Text)
	require.Equal(t, "file_opaque", payload.Input[0].OutputParts[1].FileID)
	require.Equal(t, "last", payload.Input[0].OutputParts[2].Text)
	bytes, err := json.Marshal(payload)
	require.NoError(t, err)
	var wire map[string]interface{}
	require.NoError(t, json.Unmarshal(bytes, &wire))
	require.IsType(t, []interface{}{}, wire["input"].([]interface{})[0].(map[string]interface{})["output"])
	items[1].Provider = "anthropic"
	_, err = client.ToRequest(&llm.GenerateRequest{Messages: []llm.Message{{Role: llm.RoleUser, Items: items}}})
	require.ErrorContains(t, err, "cannot resolve")
}

func TestEmptyToolOutputIsPresentAndOtherInputHasNoOutputField(t *testing.T) {
	payload := ToResponsesPayload(&Request{Messages: []Message{{Role: "user", Content: "question"}, {Role: "tool", ToolCallId: "call", Content: ""}}})
	bytes, err := json.Marshal(payload)
	require.NoError(t, err)
	var wire map[string]interface{}
	require.NoError(t, json.Unmarshal(bytes, &wire))
	inputs := wire["input"].([]interface{})
	require.Len(t, inputs, 2)
	require.NotContains(t, inputs[0].(map[string]interface{}), "output")
	require.Equal(t, "", inputs[1].(map[string]interface{})["output"])
}

// The protocol shortcut must carry the same image-detail hint as attachments,
// while leaving durable multipart content and provider-owned file IDs intact.
func TestProtocolImageDetailPreservedInOrderedResponsesOutput(t *testing.T) {
	for _, source := range []string{"data", "file"} {
		for _, tc := range []struct {
			name     string
			metadata any
			want     string
		}{
			{"high", map[string]any{"detail": "high"}, "high"},
			{"original", map[string]any{"detail": "original"}, "original"},
			{"auto", map[string]any{"detail": "auto"}, "auto"},
			{"low", map[string]any{"detail": "low"}, "low"},
			{"missing", map[string]any{"caption": "generic"}, ""},
			{"null", nil, ""},
			{"numeric", map[string]any{"detail": 17}, ""},
			{"boolean", map[string]any{"detail": true}, ""},
			{"object", map[string]any{"detail": map[string]any{"value": "high"}}, ""},
			{"array", []string{"high"}, ""},
		} {
			t.Run(source+"/"+tc.name, func(t *testing.T) {
				value := "AQID"
				if source == "file" {
					value = "file_generic_image"
				}
				raw, err := json.Marshal([]any{
					map[string]any{"type": "text", "text": "before: captureRef=generic-frame; widthPixels=64; coordinateSpace=framePixels"},
					map[string]any{"type": "image", "source": map[string]any{"type": source, "value": value, "mimeType": "image/png", "provider": "openai"}, "metadata": tc.metadata},
					map[string]any{"type": "text", "text": "after"},
				})
				require.NoError(t, err)
				items, err := clienttool.MapContent(raw)
				require.NoError(t, err)
				originalItems, err := json.Marshal(items)
				require.NoError(t, err)
				call := llm.NewToolCall("original-call", "generic_capture", map[string]any{}, "")
				request, err := (&Client{}).ToRequestContext(context.Background(), &llm.GenerateRequest{Messages: []llm.Message{
					llm.NewAssistantMessageWithToolCalls(call),
					{Role: llm.RoleTool, ToolCallId: call.ID, Items: items},
				}, Options: &llm.Options{Model: "gpt-6.1-sol"}})
				require.NoError(t, err)
				payload := ToResponsesPayload(request)
				require.Len(t, payload.Input, 2)
				require.Equal(t, "function_call", payload.Input[0].Type)
				require.Equal(t, call.ID, payload.Input[0].CallID)
				output := payload.Input[1]
				require.Equal(t, "function_call_output", output.Type)
				require.Equal(t, call.ID, output.CallID)
				require.Len(t, output.OutputParts, 3)
				require.Equal(t, "input_text", output.OutputParts[0].Type)
				require.Contains(t, output.OutputParts[0].Text, "captureRef=generic-frame")
				require.Contains(t, output.OutputParts[0].Text, "coordinateSpace=framePixels")
				require.Equal(t, "input_image", output.OutputParts[1].Type)
				require.Equal(t, tc.want, output.OutputParts[1].Detail)
				require.Equal(t, "after", output.OutputParts[2].Text)
				if source == "file" {
					require.Equal(t, value, output.OutputParts[1].FileID)
				} else {
					require.Equal(t, "data:image/png;base64,"+value, output.OutputParts[1].ImageURL)
				}
				encoded, err := json.Marshal(payload)
				require.NoError(t, err)
				require.Equal(t, 1, bytes.Count(encoded, []byte(value)), "image bytes or file ID appeared twice")
				var wire struct {
					Input []struct {
						Type   string           `json:"type"`
						CallID string           `json:"call_id"`
						Output []map[string]any `json:"output"`
					}
				}
				require.NoError(t, json.Unmarshal(encoded, &wire))
				require.Equal(t, call.ID, wire.Input[1].CallID)
				image := wire.Input[1].Output[1]
				if tc.want == "" {
					require.NotContains(t, image, "detail")
				} else {
					require.Equal(t, tc.want, image["detail"])
				}
				after, err := json.Marshal(items)
				require.NoError(t, err)
				require.Equal(t, string(originalItems), string(after), "provider projection mutated source parts")
			})
		}
	}
}
