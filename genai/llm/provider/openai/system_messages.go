package openai

// Merge only adjacent, unnamed system messages. Do not move instructions across
// conversation turns or combine content with a different instruction role.
func mergeConsecutiveSystemMessages(messages []Message) []Message {
	out := make([]Message, 0, len(messages))
	plain := func(m Message) bool {
		return m.Role == "system" && m.Name == "" && m.FunctionCall == nil && len(m.ToolCalls) == 0 && m.ToolCallId == ""
	}
	parts := func(content any) ([]ContentItem, bool) {
		switch value := content.(type) {
		case string:
			return []ContentItem{{Type: "text", Text: value}}, true
		case []ContentItem:
			return value, true
		default:
			return nil, false
		}
	}
	for _, message := range messages {
		if len(out) > 0 && plain(message) && plain(out[len(out)-1]) {
			previous := &out[len(out)-1]
			left, leftString := previous.Content.(string)
			right, rightString := message.Content.(string)
			if leftString && rightString {
				previous.Content = left + "\n\n" + right
				continue
			}
			a, aOK := parts(previous.Content)
			b, bOK := parts(message.Content)
			if aOK && bOK {
				merged := make([]ContentItem, 0, len(a)+len(b)+1)
				merged = append(merged, a...)
				merged = append(merged, ContentItem{Type: "text", Text: "\n\n"})
				previous.Content = append(merged, b...)
				continue
			}
		}
		out = append(out, message)
	}
	return out
}
