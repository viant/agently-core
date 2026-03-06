package modelcall

import (
	"fmt"
	"strings"

	"github.com/viant/agently-core/genai/llm"
)

// AssistantContentFromResponse extracts visible assistant text and whether the
// response represents a tool-producing assistant turn.
func AssistantContentFromResponse(resp *llm.GenerateResponse) (string, bool) {
	if resp == nil || len(resp.Choices) == 0 {
		return "", false
	}
	parts := make([]string, 0, len(resp.Choices))
	hasToolCalls := false
	for _, c := range resp.Choices {
		if len(c.Message.ToolCalls) > 0 || c.Message.FunctionCall != nil {
			hasToolCalls = true
		}
		if strings.Contains(strings.ToLower(c.FinishReason), "tool") {
			hasToolCalls = true
		}
		s := strings.TrimSpace(messageText(c.Message))
		if s != "" {
			parts = append(parts, s)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n")), hasToolCalls
}

// AssistantPreambleFromResponse returns a canonical preamble for a tool-producing
// assistant response. When content is empty, it synthesizes a concise tool-group
// sentence so the transcript can show execution intent during streaming.
func AssistantPreambleFromResponse(resp *llm.GenerateResponse, content string) string {
	if text := strings.TrimSpace(content); text != "" {
		return text
	}
	if resp == nil || len(resp.Choices) == 0 {
		return ""
	}
	names := make([]string, 0, 4)
	seen := map[string]struct{}{}
	for _, c := range resp.Choices {
		for _, tc := range c.Message.ToolCalls {
			name := strings.TrimSpace(tc.Name)
			if name == "" {
				continue
			}
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			names = append(names, name)
		}
		if c.Message.FunctionCall != nil {
			name := strings.TrimSpace(c.Message.FunctionCall.Name)
			if name != "" {
				if _, ok := seen[name]; !ok {
					seen[name] = struct{}{}
					names = append(names, name)
				}
			}
		}
	}
	if len(names) == 0 {
		return ""
	}
	if len(names) == 1 {
		return "Using " + names[0] + "."
	}
	if len(names) == 2 {
		return "Using " + names[0] + " and " + names[1] + "."
	}
	return fmt.Sprintf("Using %s and %d more tool(s).", strings.Join(names[:2], ", "), len(names)-2)
}
