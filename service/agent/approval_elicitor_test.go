package agent

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/service/shared/toolapproval"
)

func TestBuildApprovalRequestedSchema_UsesReviewRequestedSchemaAndSeedsDefaults(t *testing.T) {
	cfg := &llm.ApprovalConfig{
		Mode: llm.ApprovalModePrompt,
		Review: &llm.ApprovalReviewConfig{
			RequestedSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"intent": map[string]interface{}{
						"type":     "string",
						"title":    "Recommendation type",
						"readOnly": true,
					},
					"rows": map[string]interface{}{
						"type":         "array",
						"title":        "Selected recommendations",
						"x-ui-widget":  "planner.table",
						"x-ui-columns": []interface{}{map[string]interface{}{"key": "site_id", "label": "Site ID"}},
						"default":      []interface{}{},
						"items": map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"site_id":  map[string]interface{}{"type": "integer"},
								"selected": map[string]interface{}{"type": "boolean", "default": true},
							},
						},
					},
				},
				"required": []interface{}{"intent", "rows"},
			},
			Seeds: []*llm.ApprovalReviewSeed{
				{SchemaPath: "properties.intent.default", Selector: "intent"},
				{SchemaPath: "properties.rows.default", Selector: "rows"},
			},
		},
	}

	view := toolapproval.View{
		ToolName: "workspace-RecordPatch",
		Title:    "Review selected recommendations",
		Message:  "Review before patching.",
	}
	args := map[string]interface{}{
		"intent": "Target sites",
		"rows": []interface{}{
			map[string]interface{}{"site_id": 3945613211, "selected": true},
			map[string]interface{}{"site_id": 3004169891, "selected": true},
		},
	}

	got := buildApprovalRequestedSchema("workspace-RecordPatch", view, cfg, args, "Submit", "Decline", "Cancel")
	require.Equal(t, "object", got.Type)
	props := got.Properties
	require.Equal(t, "tool_approval", props["_type"].(map[string]interface{})["const"])
	intentField, ok := props["intent"].(map[string]interface{})
	require.True(t, ok)
	require.Equal(t, "Target sites", intentField["default"])
	rowsField, ok := props["rows"].(map[string]interface{})
	require.True(t, ok)
	defaultRows, ok := rowsField["default"].([]interface{})
	require.True(t, ok)
	require.Len(t, defaultRows, 2)

	rawMeta, ok := props["_approvalMeta"].(map[string]interface{})
	require.True(t, ok)
	constValue, ok := rawMeta["const"].(string)
	require.True(t, ok)
	require.Contains(t, constValue, `"type":"tool_approval"`)
}
