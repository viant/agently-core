package schema

import (
	"testing"

	"github.com/stretchr/testify/require"
	mcpschema "github.com/viant/mcp-protocol/schema"
)

func TestTypeFromInputSchema(t *testing.T) {
	input := mcpschema.ToolInputSchema{
		Type: "object",
		Properties: mcpschema.ToolInputSchemaProperties{
			"from": {"type": "string"},
			"to":   {"type": "string"},
		},
		Required: []string{"from", "to"},
	}
	rt, err := TypeFromInputSchema(input)
	require.NoError(t, err)
	require.Contains(t, rt.String(), "struct")
	require.Contains(t, rt.String(), "From string")
	require.Contains(t, rt.String(), "To string")
}

func TestGoShapeFromSchemaMap(t *testing.T) {
	schemaMap := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"active": map[string]interface{}{"type": "boolean"},
			"names": map[string]interface{}{
				"type":  "array",
				"items": map[string]interface{}{"type": "string"},
			},
		},
	}
	got, err := GoShapeFromSchemaMap(schemaMap)
	require.NoError(t, err)
	require.Contains(t, got, "struct")
	require.Contains(t, got, "Active bool")
	require.Contains(t, got, "Names  []string")
}

func TestGoShapeFromSchemaMap_NestedObjectAndDateTime(t *testing.T) {
	schemaMap := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"from": map[string]interface{}{
				"type":   "string",
				"format": "date-time",
			},
			"filters": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"siteIds": map[string]interface{}{
						"type": "array",
						"items": map[string]interface{}{
							"type": "integer",
						},
					},
					"active": map[string]interface{}{
						"type": "boolean",
					},
				},
				"required": []interface{}{"siteIds"},
			},
		},
		"required": []interface{}{"from"},
	}

	got, err := GoShapeFromSchemaMap(schemaMap)
	require.NoError(t, err)
	require.Contains(t, got, "From time.Time")
	require.Contains(t, got, "Filters struct")
	require.Contains(t, got, "SiteIds []int64")
	require.Contains(t, got, "Active  bool")
}
