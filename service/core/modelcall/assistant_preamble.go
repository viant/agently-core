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
