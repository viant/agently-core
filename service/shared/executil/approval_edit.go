package executil

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/viant/agently-core/pkg/agently/tool/resolver"
)

func ApplyApprovalEdits(args map[string]interface{}, editors []*ApprovalEditorView, editedFields map[string]interface{}) error {
	if len(args) == 0 || len(editors) == 0 || len(editedFields) == 0 {
		return nil
	}
	for _, editor := range editors {
		if editor == nil {
			continue
		}
		raw, ok := editedFields[editor.Name]
		if !ok {
			continue
		}
		nextValue, hasValue, err := resolveApprovalEditedValue(editor, raw)
		if err != nil {
			return err
		}
		if !hasValue {
			continue
		}
		if err := resolver.Assign(args, editor.Path, nextValue); err != nil {
			return fmt.Errorf("apply approval edit %s: %w", editor.Name, err)
		}
	}
	return nil
}

func extractApprovalEditedFields(payload map[string]interface{}) map[string]interface{} {
	if len(payload) == 0 {
		return nil
	}
	if raw, ok := payload["editedFields"]; ok {
		if actual, ok := raw.(map[string]interface{}); ok {
			return actual
		}
	}
	return nil
}

func resolveApprovalEditedValue(editor *ApprovalEditorView, raw interface{}) (interface{}, bool, error) {
	if editor == nil {
		return nil, false, nil
	}
	switch strings.ToLower(strings.TrimSpace(editor.Kind)) {
	case "radio_list":
		selectedID := strings.TrimSpace(fmt.Sprintf("%v", raw))
		if selectedID == "" {
			return nil, false, nil
		}
		for _, option := range editor.Options {
			if option != nil && option.ID == selectedID {
				return cloneApprovalValue(option.Item), true, nil
			}
		}
		return nil, false, nil
	case "checkbox_list":
		selected := normalizeApprovalSelectionList(raw)
		items := make([]interface{}, 0, len(selected))
		for _, option := range editor.Options {
			if option == nil {
				continue
			}
			if _, ok := selected[option.ID]; !ok {
				continue
			}
			items = append(items, cloneApprovalValue(option.Item))
		}
		return items, true, nil
	default:
		return raw, true, nil
	}
}

func normalizeApprovalSelectionList(raw interface{}) map[string]struct{} {
	result := map[string]struct{}{}
	switch actual := raw.(type) {
	case []interface{}:
		for _, item := range actual {
			key := strings.TrimSpace(fmt.Sprintf("%v", item))
			if key != "" {
				result[key] = struct{}{}
			}
		}
	case []string:
		for _, item := range actual {
			key := strings.TrimSpace(item)
			if key != "" {
				result[key] = struct{}{}
			}
		}
	default:
		key := strings.TrimSpace(fmt.Sprintf("%v", actual))
		if key != "" {
			result[key] = struct{}{}
		}
	}
	return result
}

func cloneApprovalValue(value interface{}) interface{} {
	if value == nil {
		return nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var cloned interface{}
	if err := json.Unmarshal(data, &cloned); err != nil {
		return value
	}
	return cloned
}
