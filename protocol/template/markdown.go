package template

import (
	"fmt"
	"strings"

	"github.com/viant/forge/backend/authoring/markdown"
	"gopkg.in/yaml.v3"
)

// MarkdownSource compiles literal instructions and named contract blocks through
// Forge's shared Markdown parser. Ordinary fenced examples remain instructions.
func MarkdownSource(raw []byte) ([]byte, string, error) {
	metadata, body, err := markdown.SplitFrontmatter(raw)
	if err != nil {
		return nil, "", err
	}
	var fields map[string]any
	if err := yaml.Unmarshal(metadata, &fields); err != nil {
		return nil, "", err
	}
	if fields == nil {
		fields = map[string]any{}
	}
	text, blocks, err := markdown.ExtractFences(body, "template-schema", "template-fences", "template-examples")
	if err != nil {
		return nil, "", err
	}
	for _, key := range []string{"schema", "fences", "examples"} {
		block, exists := blocks["template-"+key]
		if !exists {
			continue
		}
		if _, exists := fields[key]; exists {
			return nil, "", fmt.Errorf("template Markdown repeats %s", key)
		}
		var value any
		if err := yaml.Unmarshal(block, &value); err != nil {
			return nil, "", fmt.Errorf("template-%s: %w", key, err)
		}
		fields[key] = value
	}
	if strings.TrimSpace(text) != "" {
		if _, exists := fields["instructions"]; exists {
			return nil, "", fmt.Errorf("template instructions conflict with Markdown body")
		}
		fields["instructions"] = text
	}
	encoded, err := yaml.Marshal(fields)
	return encoded, text, err
}
