package debugtrace

import (
	"encoding/json"
	"strings"

	"github.com/viant/agently-core/genai/llm"
)

func SummarizeMessages(messages []llm.Message) []map[string]any {
	if len(messages) == 0 {
		return nil
	}
	result := make([]map[string]any, 0, len(messages))
	for _, msg := range messages {
		item := map[string]any{
			"role":        strings.TrimSpace(string(msg.Role)),
			"contentLen":  len(messageText(msg)),
			"toolCallID":  strings.TrimSpace(msg.ToolCallId),
			"toolCalls":   SummarizeToolCalls(msg.ToolCalls),
			"contentHead": head(messageText(msg), 200),
		}
		if name := strings.TrimSpace(msg.Name); name != "" {
			item["name"] = name
		}
		result = append(result, item)
	}
	return result
}

func SummarizeToolCalls(calls []llm.ToolCall) []map[string]any {
	if len(calls) == 0 {
		return nil
	}
	result := make([]map[string]any, 0, len(calls))
	for _, call := range calls {
		item := map[string]any{
			"id":      strings.TrimSpace(call.ID),
			"name":    strings.TrimSpace(call.Name),
			"args":    stableJSON(call.Arguments),
			"result":  head(strings.TrimSpace(call.Result), 200),
			"error":   head(strings.TrimSpace(call.Error), 200),
			"hasArgs": len(call.Arguments) > 0,
		}
		result = append(result, item)
	}
	return result
}

func stableJSON(value any) string {
	if value == nil {
		return ""
	}
	data, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(data)
}

func messageText(msg llm.Message) string {
	if text := strings.TrimSpace(msg.Content); text != "" {
		return text
	}
	var parts []string
	for _, item := range msg.Items {
		if text := strings.TrimSpace(item.Text); text != "" {
			parts = append(parts, text)
			continue
		}
		if text := strings.TrimSpace(item.Data); text != "" {
			parts = append(parts, text)
		}
	}
	for _, item := range msg.ContentItems {
		if text := strings.TrimSpace(item.Text); text != "" {
			parts = append(parts, text)
			continue
		}
		if text := strings.TrimSpace(item.Data); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func head(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return value[:limit]
}
