package modelcall

import (
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

// AssistantPreambleFromResponse returns model-authored preamble text for a
// tool-producing assistant response. It must not synthesize text from tool
// metadata; provisional bubbles should reflect only what the model actually said.
func AssistantPreambleFromResponse(resp *llm.GenerateResponse, content string) string {
	return strings.TrimSpace(content)
}

// synthesizeToolPreamble builds a human-readable preamble from the tool call
// names in the response. Used when the model produces tool calls without any
// accompanying text, so the interim assistant message still exists in the
// transcript for parent_message_id linking.
func synthesizeToolPreamble(resp *llm.GenerateResponse) string {
	if resp == nil {
		return "Executing tool calls."
	}
	seen := map[string]struct{}{}
	var names []string
	for _, c := range resp.Choices {
		for _, tc := range c.Message.ToolCalls {
			name := strings.TrimSpace(tc.Name)
			if name == "" {
				name = strings.TrimSpace(tc.Function.Name)
			}
			if name == "" {
				continue
			}
			// Use the short display name (last segment after / or -)
			display := name
			if idx := strings.LastIndexAny(display, "/-"); idx >= 0 && idx+1 < len(display) {
				display = display[idx+1:]
			}
			if _, ok := seen[display]; ok {
				continue
			}
			seen[display] = struct{}{}
			names = append(names, display)
		}
	}
	if len(names) == 0 {
		return "Executing tool calls."
	}
	return "Calling " + strings.Join(names, ", ") + "."
}
