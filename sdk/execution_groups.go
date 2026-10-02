package sdk

import (
	"sort"
	"strconv"
	"strings"

	convstore "github.com/viant/agently-core/app/store/conversation"
	conversationmodel "github.com/viant/agently-core/model/conversation"
)

func indexToolMessagesByParentAndIteration(turn *convstore.Turn) map[string][]*conversationmodel.ToolMessageView {
	if turn == nil || len(turn.Message) == 0 {
		return nil
	}
	out := map[string][]*conversationmodel.ToolMessageView{}
	for _, message := range turn.Message {
		if message == nil || len(message.ToolMessage) == 0 {
			continue
		}
		parentID := strings.TrimSpace(message.Id)
		if parentID == "" {
			continue
		}
		for _, toolMessage := range message.ToolMessage {
			if toolMessage == nil {
				continue
			}
			key := toolMessageGroupKey(parentID, toolMessage.Iteration)
			out[key] = append(out[key], toolMessage)
		}
	}
	return out
}

func executionNarration(message *conversationmodel.MessageView) string {
	if message == nil {
		return ""
	}
	if narration := visibleContentOrEmpty(message.Narration); narration != "" {
		return narration
	}
	if message.Interim == 1 {
		return visibleContentOrEmpty(message.Content)
	}
	return ""
}

func isFinalExecutionMessage(message *conversationmodel.MessageView) bool {
	if message == nil {
		return false
	}
	if strings.ToLower(strings.TrimSpace(message.Role)) != "assistant" {
		return false
	}
	if message.Interim != 0 {
		return false
	}
	return strings.TrimSpace(stringValue(message.Content)) != ""
}

func collectToolChildren(turn *convstore.Turn, message *conversationmodel.MessageView, indexed map[string][]*conversationmodel.ToolMessageView) ([]*conversationmodel.ToolMessageView, []*conversationmodel.ToolCallView) {
	if message == nil {
		return nil, nil
	}
	toolMessages := make([]*conversationmodel.ToolMessageView, 0, len(message.ToolMessage))
	for _, toolMessage := range message.ToolMessage {
		if toolMessage != nil {
			toolMessages = append(toolMessages, toolMessage)
		}
	}
	parentID := strings.TrimSpace(stringValue(message.ParentMessageId))
	if parentID == "" {
		parentID = strings.TrimSpace(message.Id)
	}
	key := toolMessageGroupKey(parentID, message.Iteration)
	appendToolMessage := func(toolMessage *conversationmodel.ToolMessageView) {
		if toolMessage == nil {
			return
		}
		for _, existing := range toolMessages {
			if existing != nil && existing.Id == toolMessage.Id {
				return
			}
		}
		toolMessages = append(toolMessages, toolMessage)
	}
	for _, toolMessage := range indexed[key] {
		appendToolMessage(toolMessage)
	}
	if message.Iteration != nil {
		for _, toolMessage := range indexed[toolMessageGroupKey(parentID, nil)] {
			appendToolMessage(toolMessage)
		}
	}
	if len(toolMessages) == 0 {
		return nil, nil
	}
	sort.SliceStable(toolMessages, func(i, j int) bool {
		return lessToolMessage(toolMessages[i], toolMessages[j])
	})
	toolCalls := make([]*conversationmodel.ToolCallView, 0, len(toolMessages))
	for _, toolMessage := range toolMessages {
		if toolMessage.ToolCall != nil {
			toolCalls = append(toolCalls, toolMessage.ToolCall)
		}
	}
	return toolMessages, toolCalls
}

func toolMessageGroupKey(parentID string, iteration *int) string {
	return parentID + "::" + iterationKey(iteration)
}

func iterationKey(iteration *int) string {
	if iteration == nil {
		return ""
	}
	return strconv.Itoa(*iteration)
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
