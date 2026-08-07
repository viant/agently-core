package converse

import (
	"encoding/json"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
)

func TestToRequest_MapsSystemToolsAndToolResults(t *testing.T) {
	request := &llm.GenerateRequest{
		Instructions: "primary instructions",
		Messages: []llm.Message{
			llm.NewSystemMessage("system context"),
			llm.NewUserMessage("find the weather"),
			{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call-1", Name: "weather", Arguments: map[string]interface{}{"city": "Warsaw"}}}},
			{Role: llm.RoleTool, Name: "weather", ToolCallId: "call-1", Content: "sunny"},
		},
		Options: &llm.Options{
			MaxTokens: 512,
			Tools: []llm.Tool{llm.NewFunctionTool(llm.ToolDefinition{
				Name: "weather", Description: "Get weather", Parameters: map[string]interface{}{"city": map[string]interface{}{"type": "string"}}, Required: []string{"city"},
			})},
		},
	}
	got, err := toRequest(request, &Client{MaxTokens: 100})
	require.NoError(t, err)
	require.Len(t, got.System, 1)
	require.Contains(t, got.System[0].(*types.SystemContentBlockMemberText).Value, "primary instructions")
	require.Len(t, got.Messages, 3)

	toolUse := got.Messages[1].Content[0].(*types.ContentBlockMemberToolUse).Value
	require.Equal(t, "call-1", aws.ToString(toolUse.ToolUseId))
	require.NotNil(t, toolUse.Input)
	toolInputJSON, err := toolUse.Input.MarshalSmithyDocument()
	require.NoError(t, err)
	var toolInput map[string]interface{}
	require.NoError(t, json.Unmarshal(toolInputJSON, &toolInput))
	require.Equal(t, "Warsaw", toolInput["city"])

	toolResult := got.Messages[2].Content[0].(*types.ContentBlockMemberToolResult).Value
	require.Equal(t, "call-1", aws.ToString(toolResult.ToolUseId))
	require.Equal(t, "sunny", toolResult.Content[0].(*types.ToolResultContentBlockMemberText).Value)
	require.NotNil(t, got.ToolConfig)
	require.Len(t, got.ToolConfig.Tools, 1)
	toolSpec := got.ToolConfig.Tools[0].(*types.ToolMemberToolSpec).Value
	inputSchema := toolSpec.InputSchema.(*types.ToolInputSchemaMemberJson).Value
	inputSchemaJSON, err := inputSchema.MarshalSmithyDocument()
	require.NoError(t, err)
	var schema map[string]interface{}
	require.NoError(t, json.Unmarshal(inputSchemaJSON, &schema))
	require.Contains(t, schema["properties"], "city")
	require.EqualValues(t, 512, aws.ToInt32(got.InferenceConfig.MaxTokens))
}

func TestFromMessage_MapsToolUse(t *testing.T) {
	message := types.Message{Role: types.ConversationRoleAssistant, Content: []types.ContentBlock{
		&types.ContentBlockMemberText{Value: "checking"},
		&types.ContentBlockMemberToolUse{Value: types.ToolUseBlock{
			ToolUseId: aws.String("call-2"), Name: aws.String("find"), Input: document.NewLazyDocument(map[string]interface{}{"query": "needle"}),
		}},
	}}
	got := fromMessage(message)
	require.Equal(t, "checking", got.Content)
	require.Len(t, got.ToolCalls, 1)
	require.Equal(t, "call-2", got.ToolCalls[0].ID)
	require.Equal(t, "needle", got.ToolCalls[0].Arguments["query"])
}
