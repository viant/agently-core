package toolapproval

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/viant/agently-core/genai/llm"
)

func TestBuildView_SystemOSGetEnv(t *testing.T) {
	view := BuildView("system/os:getEnv", map[string]interface{}{
		"names": []interface{}{"USER", "HOME"},
	}, &llm.ApprovalConfig{Mode: llm.ApprovalModePrompt})

	assert.Equal(t, "OS Env Access", view.Title)
	assert.Equal(t, "The agent wants access to your USER and HOME environment variables.", view.Message)
	assert.Equal(t, map[string]interface{}{"names": []string{"USER", "HOME"}}, view.Data)
}

func TestBuildView_RecordCollectionEditors(t *testing.T) {
	view := BuildView("records/select", map[string]interface{}{
		"records": []interface{}{
			map[string]interface{}{"id": "r1", "label": "Record 1", "description": "first"},
			map[string]interface{}{"id": "r2", "label": "Record 2", "description": "second"},
		},
	}, &llm.ApprovalConfig{
		UI: &llm.ApprovalUIBinding{
			Editable: []*llm.ApprovalEditableField{
				{
					Name:                    "records",
					Selector:                "input.records",
					Kind:                    "radio_list",
					Label:                   "Records",
					ItemValueSelector:       "id",
					ItemLabelSelector:       "label",
					ItemDescriptionSelector: "description",
				},
			},
		},
	})

	if assert.Len(t, view.Editors, 1) {
		assert.Equal(t, "records", view.Editors[0].Name)
		assert.Equal(t, "records", view.Editors[0].Path)
		if assert.Len(t, view.Editors[0].Options, 2) {
			assert.Equal(t, "r1", view.Editors[0].Options[0].ID)
			assert.Equal(t, "Record 1", view.Editors[0].Options[0].Label)
			assert.Equal(t, "first", view.Editors[0].Options[0].Description)
			assert.Equal(t, "r2", view.Editors[0].Options[1].ID)
			assert.Equal(t, "Record 2", view.Editors[0].Options[1].Label)
			assert.Equal(t, "second", view.Editors[0].Options[1].Description)
		}
	}
}

func TestApplyEdits(t *testing.T) {
	t.Run("checkbox list rewrites selected items", func(t *testing.T) {
		args := map[string]interface{}{
			"names": []interface{}{"HOME", "SHELL", "PATH"},
		}
		editors := []*EditorView{
			{
				Name: "names",
				Kind: "checkbox_list",
				Path: "names",
				Options: []*OptionView{
					{ID: "HOME", Item: "HOME"},
					{ID: "SHELL", Item: "SHELL"},
					{ID: "PATH", Item: "PATH"},
				},
			},
		}

		err := ApplyEdits(args, editors, map[string]interface{}{"names": []interface{}{"HOME", "PATH"}})
		assert.NoError(t, err)
		assert.EqualValues(t, []interface{}{"HOME", "PATH"}, args["names"])
	})

	t.Run("radio list rewrites selected record", func(t *testing.T) {
		args := map[string]interface{}{
			"record": map[string]interface{}{"id": "r1", "label": "Record 1"},
		}
		editors := []*EditorView{
			{
				Name: "record",
				Kind: "radio_list",
				Path: "record",
				Options: []*OptionView{
					{ID: "r1", Item: map[string]interface{}{"id": "r1", "label": "Record 1"}},
					{ID: "r2", Item: map[string]interface{}{"id": "r2", "label": "Record 2"}},
				},
			},
		}

		err := ApplyEdits(args, editors, map[string]interface{}{"record": "r2"})
		assert.NoError(t, err)
		assert.EqualValues(t, map[string]interface{}{"id": "r2", "label": "Record 2"}, args["record"])
	})
}

func TestApplyReview_GroupRows(t *testing.T) {
	args := map[string]interface{}{
		"rows": []interface{}{
			map[string]interface{}{
				"publisher_id": 37,
				"site_id":      3945613211,
				"location":     "37/3945613211",
				"audience_id":  7301206,
				"relationship": "target",
				"selected":     true,
			},
			map[string]interface{}{
				"publisher_id": 48,
				"site_id":      3004169891,
				"location":     "48/3004169891",
				"audience_id":  7301206,
				"relationship": "target",
				"selected":     true,
			},
			map[string]interface{}{
				"publisher_id": 509,
				"site_id":      3966455595,
				"location":     "509/3966455595",
				"audience_id":  7301206,
				"relationship": "exclusion",
				"selected":     true,
			},
		},
		"intent": "submit_target",
	}
	review := &llm.ApprovalReviewConfig{
		XForm: map[string]interface{}{
			"type":            "group_rows",
			"rowsField":       "rows",
			"selectionField":  "selected",
			"groupBy":         "relationship",
			"valueField":      "location",
			"audienceIdField": "audience_id",
			"feature":         "publisher",
			"writePath":       "recommendation",
			"intentField":     "intent",
			"intentGroupMap": map[string]interface{}{
				"submit_target":    "target",
				"submit_exclusion": "exclusion",
			},
			"groups": map[string]interface{}{
				"target": map[string]interface{}{
					"mode":              "ADD",
					"selectorDirection": "INCLUDE",
					"targetField":       "target",
				},
				"exclusion": map[string]interface{}{
					"mode":              "EXCLUDE",
					"selectorDirection": "EXCLUDE",
					"targetField":       "exclusion",
				},
			},
		},
	}

	err := ApplyReview(args, review, args)
	assert.NoError(t, err)
	assert.NotContains(t, args, "rows")
	assert.NotContains(t, args, "intent")
	assert.EqualValues(t, map[string]interface{}{
		"audience_id":        7301206,
		"mode":               "ADD",
		"selector_direction": "INCLUDE",
		"target_field":       "target",
		"proposed_value": map[string]interface{}{
			"target": map[string]interface{}{
				"clauses": []interface{}{
					map[string]interface{}{
						"feature": "publisher",
						"values":  []interface{}{"37/3945613211", "48/3004169891"},
					},
				},
			},
		},
	}, args["recommendation"])
}
