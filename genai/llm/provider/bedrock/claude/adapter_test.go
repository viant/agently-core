package claude

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
)

func TestToRequestToolUseIncludesEmptyInput(t *testing.T) {
	request, err := ToRequest(context.Background(), &llm.GenerateRequest{
		Messages: []llm.Message{
			{
				Role: llm.RoleAssistant,
				ToolCalls: []llm.ToolCall{{
					ID:   "call_1",
					Name: "message-add",
				}},
			},
		},
	})
	require.NoError(t, err)

	data, err := json.Marshal(request)
	require.NoError(t, err)

	var payload map[string]interface{}
	require.NoError(t, json.Unmarshal(data, &payload))
	messages, ok := payload["messages"].([]interface{})
	require.True(t, ok)
	require.Len(t, messages, 1)
	message := messages[0].(map[string]interface{})
	content := message["content"].([]interface{})
	block := content[0].(map[string]interface{})

	require.EqualValues(t, "tool_use", block["type"])
	require.EqualValues(t, "call_1", block["id"])
	require.EqualValues(t, "message-add", block["name"])
	require.Contains(t, block, "input")
	require.EqualValues(t, map[string]interface{}{}, block["input"])
}
