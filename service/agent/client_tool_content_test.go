package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/genai/llm"
	conversationmodel "github.com/viant/agently-core/model/conversation"
	"github.com/viant/agently-core/runtime/clienttool"
)

func TestOrderedUserAndToolPartsSurviveNativeHistoryBinding(t *testing.T) {
	raw := json.RawMessage(`[{"type":"text","text":"before","metadata":{"counter":9007199254740993,"decimal":1.0000000000000000001}},{"type":"image","source":{"type":"data","value":"AQID","mimeType":"image/png"}},{"type":"text","text":"after"}]`)
	items, err := clienttool.MapContent(raw)
	require.NoError(t, err)
	userRaw, err := json.Marshal(items)
	require.NoError(t, err)
	userMarker, toolMarker := clienttool.ItemsMIME, clienttool.ContentMIME
	userBody, toolBody := string(userRaw), string(raw)
	turn := "turn"
	user := &conversationmodel.MessageView{Id: "user", Role: "user", Type: "task", TurnId: &turn, RawContent: &userBody, ContextSummary: &userMarker}
	tool := toolOpMessage("tool", "call", "completed", 1, toolBody)
	tool.ContextSummary = &toolMarker
	tool.MessageToolCall.ToolName = "frontend"
	service := &Service{}
	history, err := service.buildHistory(context.Background(), apiconv.Transcript{&apiconv.Turn{Id: turn, Message: []*conversationmodel.MessageView{user, tool}}})
	require.NoError(t, err)
	messages := history.LLMMessages()
	var userMessage, toolMessage *llm.Message
	for i := range messages {
		switch messages[i].Role {
		case llm.RoleUser:
			userMessage = &messages[i]
		case llm.RoleTool:
			toolMessage = &messages[i]
		}
	}
	require.NotNil(t, userMessage)
	require.Equal(t, items, userMessage.Items)
	require.Equal(t, json.Number("9007199254740993"), userMessage.Items[0].Metadata["counter"])
	require.Equal(t, json.Number("1.0000000000000000001"), userMessage.Items[0].Metadata["decimal"])
	require.NotNil(t, toolMessage)
	require.Equal(t, items, toolMessage.Items)
	require.Equal(t, "call", toolMessage.ToolCallId)
}
