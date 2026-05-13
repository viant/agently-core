package claude

import "encoding/json"

// MarshalJSON keeps Bedrock Claude tool_use blocks valid when a persisted tool
// call has no arguments. Bedrock requires input to be present, even when empty.
func (b ContentBlock) MarshalJSON() ([]byte, error) {
	if b.Type != "tool_use" {
		type contentBlock ContentBlock
		return json.Marshal(contentBlock(b))
	}
	input := b.Input
	if input == nil {
		input = map[string]interface{}{}
	}
	out := map[string]interface{}{
		"type":  b.Type,
		"input": input,
	}
	if b.ID != "" {
		out["id"] = b.ID
	}
	if b.Name != "" {
		out["name"] = b.Name
	}
	if b.CacheControl != nil {
		out["cache_control"] = b.CacheControl
	}
	return json.Marshal(out)
}
