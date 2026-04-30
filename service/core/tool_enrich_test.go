package core

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
)

func TestEnrichToolDefinition_NormalizesSchemaNodes(t *testing.T) {
	t.Setenv("AGENTLY_WORKSPACE", filepath.Join(t.TempDir(), ".agently"))

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

func TestEnrichToolDefinition_CopiesRequiredIntoParameterSchema(t *testing.T) {
	t.Setenv("AGENTLY_WORKSPACE", filepath.Join(t.TempDir(), ".agently"))

	svc := &Service{}
	def := &llm.ToolDefinition{
		Name:     "test/tool:run",
		Required: []string{"from", "to"},
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"from": map[string]any{"type": "string"},
				"to":   map[string]any{"type": "string"},
			},
		},
	}

	svc.EnrichToolDefinition(def)

	required, ok := def.Parameters["required"].([]interface{})
	require.True(t, ok)
	require.Equal(t, []interface{}{"from", "to"}, required)
}
