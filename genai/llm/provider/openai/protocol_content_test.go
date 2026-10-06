package openai

import (
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
