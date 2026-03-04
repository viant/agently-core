package core

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
)

func TestEnrichToolDefinition_NormalizesSchemaNodes(t *testing.T) {
	svc := &Service{}
	def := &llm.ToolDefinition{
		Name: "test/tool:run",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"labels": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "string",
					},
				},
				"config": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"enabled": map[string]any{"type": "boolean"},
					},
				},
			},
		},
	}

	svc.EnrichToolDefinition(def)

	labels := def.Parameters["properties"].(map[string]any)["labels"].(map[string]any)
	require.Equal(t, "tags", labels["x-ui-widget"])
	require.Equal(t, []any{}, labels["default"])

	config := def.Parameters["properties"].(map[string]any)["config"].(map[string]any)
	require.Equal(t, map[string]any{}, config["default"])
}
