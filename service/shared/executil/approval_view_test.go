package executil

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/viant/agently-core/genai/llm"
)

func TestBuildApprovalView_SystemOSGetEnv(t *testing.T) {
	view := BuildApprovalView("system/os:getEnv", map[string]interface{}{
		"names": []interface{}{"USER", "HOME"},
	}, &llm.ApprovalConfig{Mode: llm.ApprovalModePrompt})

	assert.Equal(t, "OS Env Access", view.Title)
	assert.Equal(t, "The agent wants access to your USER and HOME environment variables.", view.Message)
	assert.Equal(t, map[string]interface{}{"names": []string{"USER", "HOME"}}, view.Data)
}

func TestBuildApprovalView_RecordCollectionEditors(t *testing.T) {
	view := BuildApprovalView("records/select", map[string]interface{}{
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

func TestApplyApprovalEdits(t *testing.T) {
	t.Run("checkbox list rewrites selected items", func(t *testing.T) {
		args := map[string]interface{}{
			"names": []interface{}{"HOME", "SHELL", "PATH"},
		}
		editors := []*ApprovalEditorView{
			{
				Name: "names",
				Kind: "checkbox_list",
				Path: "names",
				Options: []*ApprovalOptionView{
					{ID: "HOME", Item: "HOME"},
					{ID: "SHELL", Item: "SHELL"},
					{ID: "PATH", Item: "PATH"},
				},
			},
		}

		err := ApplyApprovalEdits(args, editors, map[string]interface{}{"names": []interface{}{"HOME", "PATH"}})
		assert.NoError(t, err)
		assert.EqualValues(t, []interface{}{"HOME", "PATH"}, args["names"])
	})

	t.Run("radio list rewrites selected record", func(t *testing.T) {
		args := map[string]interface{}{
			"record": map[string]interface{}{"id": "r1", "label": "Record 1"},
		}
		editors := []*ApprovalEditorView{
			{
				Name: "record",
				Kind: "radio_list",
				Path: "record",
				Options: []*ApprovalOptionView{
					{ID: "r1", Item: map[string]interface{}{"id": "r1", "label": "Record 1"}},
					{ID: "r2", Item: map[string]interface{}{"id": "r2", "label": "Record 2"}},
				},
			},
		}

		err := ApplyApprovalEdits(args, editors, map[string]interface{}{"record": "r2"})
		assert.NoError(t, err)
		assert.EqualValues(t, map[string]interface{}{"id": "r2", "label": "Record 2"}, args["record"])
	})
}
