package agui

// SubagentHasText reports whether this causally registered invocation already
// emitted text, allowing a preset return to provide fallback content once.
func (t *Translator) SubagentHasText(invocationID string) bool {
	child := t.children[invocationID]
	return child != nil && child.translator.HasText()
}

// HasToolCall checks the run graph, including calls authored by descendants.
func (t *Translator) HasToolCall(nativeTurnID, nativeCallID string) bool {
	id := t.toolID(nativeTurnID, nativeCallID)
	if t.tools[id] != nil {
		return true
	}
	for _, child := range t.children {
		if child.translator.HasToolCall(nativeTurnID, nativeCallID) {
			return true
		}
	}
	return false
}
