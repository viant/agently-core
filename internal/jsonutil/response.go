package jsonutil

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// EnsureJSONResponse extracts and unmarshals valid JSON content from a given string into the target interface.
// It trims potential code block markers and identifies the JSON object or array to parse.
// Returns an error if unmarshalling fails. If no JSON payload is found, it returns nil.
func EnsureJSONResponse(ctx context.Context, text string, target interface{}) error {
	_ = ctx
	if start := strings.Index(text, "```json"); start != -1 {
		fragment := text[start+len("```json"):]
		if end := strings.Index(fragment, "```"); end != -1 {
			text = fragment[:end]
		}
	} else if start := strings.Index(text, "```"); start != -1 {
		fragment := text[start+3:]
		if end := strings.Index(fragment, "```"); end != -1 {
			text = fragment[:end]
		}
	}

	text = strings.TrimSpace(text)

	objectStart := strings.Index(text, "{")
	objectEnd := strings.LastIndex(text, "}")
	arrayStart := strings.Index(text, "[")
	arrayEnd := strings.LastIndex(text, "]")

	switch {
	case objectStart != -1 && objectEnd != -1 && (arrayStart == -1 || objectStart < arrayStart):
		text = text[objectStart : objectEnd+1]
	case arrayStart != -1 && arrayEnd != -1:
		text = text[arrayStart : arrayEnd+1]
	default:
		return nil
	}

	if err := json.Unmarshal([]byte(text), target); err != nil {
		return fmt.Errorf("failed to unmarshal LLM text into %T: %w\nRaw text: %s", target, err, text)
	}
	return nil
}
