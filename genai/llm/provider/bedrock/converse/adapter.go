package converse

import (
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/viant/agently-core/genai/llm"
)

type requestParts struct {
	Messages        []types.Message
	System          []types.SystemContentBlock
	InferenceConfig *types.InferenceConfiguration
	ToolConfig      *types.ToolConfiguration
}

func toRequest(request *llm.GenerateRequest, defaults *Client) (*requestParts, error) {
	if request == nil {
		return nil, fmt.Errorf("request is required")
	}
	ret := &requestParts{}
	var systemParts []string
	if strings.TrimSpace(request.Instructions) != "" {
		systemParts = append(systemParts, strings.TrimSpace(request.Instructions))
	}
	for _, message := range request.Messages {
		if message.Role == llm.RoleSystem {
			if text := llm.MessageText(message); text != "" {
				systemParts = append(systemParts, text)
			}
			continue
		}
		converted, err := toMessage(message)
		if err != nil {
			return nil, err
		}
		ret.Messages = append(ret.Messages, converted)
	}
	if len(systemParts) > 0 {
		ret.System = []types.SystemContentBlock{&types.SystemContentBlockMemberText{Value: strings.Join(systemParts, "\n\n")}}
	}
	ret.InferenceConfig = inferenceConfig(request.Options, defaults)
	ret.ToolConfig = toolConfig(request.Options)
	return ret, nil
}

func toMessage(message llm.Message) (types.Message, error) {
	role := types.ConversationRoleUser
	if message.Role == llm.RoleAssistant {
		role = types.ConversationRoleAssistant
	}
	var content []types.ContentBlock
	if text := llm.MessageText(message); text != "" && message.Role != llm.RoleTool && message.Role != llm.RoleFunction {
		content = append(content, &types.ContentBlockMemberText{Value: text})
	}
	for _, call := range message.ToolCalls {
		id := strings.TrimSpace(call.ID)
		if id == "" {
			id = call.Name
		}
		content = append(content, &types.ContentBlockMemberToolUse{Value: types.ToolUseBlock{
			ToolUseId: aws.String(id), Name: aws.String(call.Name), Input: document.NewLazyDocument(call.Arguments),
		}})
	}
	if message.Role == llm.RoleTool || message.Role == llm.RoleFunction {
		id := strings.TrimSpace(message.ToolCallId)
		if id == "" {
			return types.Message{}, fmt.Errorf("tool result for %q is missing tool call id", message.Name)
		}
		content = append(content, &types.ContentBlockMemberToolResult{Value: types.ToolResultBlock{
			ToolUseId: aws.String(id),
			Content:   []types.ToolResultContentBlock{&types.ToolResultContentBlockMemberText{Value: llm.MessageText(message)}},
		}})
	}
	if len(content) == 0 {
		content = append(content, &types.ContentBlockMemberText{Value: " "})
	}
	return types.Message{Role: role, Content: content}, nil
}

func inferenceConfig(options *llm.Options, defaults *Client) *types.InferenceConfiguration {
	ret := &types.InferenceConfiguration{}
	maxTokens := defaults.MaxTokens
	if options != nil && options.MaxTokens > 0 {
		maxTokens = options.MaxTokens
	}
	if maxTokens > 0 {
		value := int32(maxTokens)
		ret.MaxTokens = &value
	}
	if options != nil {
		if options.Temperature != 0 {
			value := float32(options.Temperature)
			ret.Temperature = &value
		} else if defaults.Temperature != nil {
			value := float32(*defaults.Temperature)
			ret.Temperature = &value
		}
		if options.TopP > 0 {
			value := float32(options.TopP)
			ret.TopP = &value
		}
		ret.StopSequences = options.StopWords
	} else if defaults.Temperature != nil {
		value := float32(*defaults.Temperature)
		ret.Temperature = &value
	}
	if ret.MaxTokens == nil && ret.Temperature == nil && ret.TopP == nil && len(ret.StopSequences) == 0 {
		return nil
	}
	return ret
}

func toolConfig(options *llm.Options) *types.ToolConfiguration {
	if options == nil || len(options.Tools) == 0 || options.ToolChoice.Type == "none" {
		return nil
	}
	ret := &types.ToolConfiguration{}
	for _, tool := range options.Tools {
		def := tool.Definition
		if def.Parameters != nil {
			_, hasType := def.Parameters["type"]
			_, hasProperties := def.Parameters["properties"]
			if !hasType && !hasProperties {
				def.Parameters = map[string]interface{}{
					"type":       "object",
					"properties": def.Parameters,
				}
			}
		}
		def.Normalize()
		name := def.Name
		if name == "" {
			name = tool.Name
		}
		if name == "" {
			continue
		}
		schema := def.Parameters
		ret.Tools = append(ret.Tools, &types.ToolMemberToolSpec{Value: types.ToolSpecification{
			Name: aws.String(name), Description: aws.String(def.Description), Strict: aws.Bool(def.Strict),
			InputSchema: &types.ToolInputSchemaMemberJson{Value: document.NewLazyDocument(schema)},
		}})
	}
	if len(ret.Tools) == 0 {
		return nil
	}
	ret.ToolChoice = &types.ToolChoiceMemberAuto{}
	if options.ToolChoice.Type == "function" && options.ToolChoice.Function != nil {
		ret.ToolChoice = &types.ToolChoiceMemberTool{Value: types.SpecificToolChoice{Name: aws.String(options.ToolChoice.Function.Name)}}
	}
	return ret
}

func fromMessage(message types.Message) llm.Message {
	ret := llm.Message{Role: llm.RoleAssistant}
	for _, block := range message.Content {
		switch actual := block.(type) {
		case *types.ContentBlockMemberText:
			ret.Content += actual.Value
			ret.Items = append(ret.Items, llm.NewTextContent(actual.Value))
		case *types.ContentBlockMemberToolUse:
			arguments := map[string]interface{}{}
			if actual.Value.Input != nil {
				_ = actual.Value.Input.UnmarshalSmithyDocument(&arguments)
			}
			ret.ToolCalls = append(ret.ToolCalls, llm.ToolCall{
				ID: aws.ToString(actual.Value.ToolUseId), Name: aws.ToString(actual.Value.Name), Arguments: arguments, Type: "function",
			})
		}
	}
	return ret
}

func usageFromAWS(value *types.TokenUsage) *llm.Usage {
	if value == nil {
		return nil
	}
	return &llm.Usage{
		PromptTokens: int(aws.ToInt32(value.InputTokens)), CompletionTokens: int(aws.ToInt32(value.OutputTokens)),
		TotalTokens: int(aws.ToInt32(value.TotalTokens)), CachedTokens: int(aws.ToInt32(value.CacheReadInputTokens)),
	}
}
