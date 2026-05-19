package toolapproval

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/pkg/agently/tool/resolver"
)

func ApplyEdits(args map[string]interface{}, editors []*EditorView, editedFields map[string]interface{}) error {
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
		nextValue, hasValue, err := resolveEditedValue(editor, raw)
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

func ExtractEditedFields(payload map[string]interface{}) map[string]interface{} {
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

func ApplyReview(args map[string]interface{}, review *llm.ApprovalReviewConfig, payload map[string]interface{}) error {
	if len(args) == 0 || review == nil || len(review.XForm) == 0 || len(payload) == 0 {
		return nil
	}
	typeName := strings.ToLower(strings.TrimSpace(stringValue(review.XForm["type"])))
	switch typeName {
	case "", "group_rows":
		return applyGroupRowsReview(args, review.XForm, payload)
	default:
		return fmt.Errorf("unsupported approval review xform type %q", typeName)
	}
}

func resolveEditedValue(editor *EditorView, raw interface{}) (interface{}, bool, error) {
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
				return cloneValue(option.Item), true, nil
			}
		}
		return nil, false, nil
	case "checkbox_list":
		selected := normalizeSelectionList(raw)
		items := make([]interface{}, 0, len(selected))
		for _, option := range editor.Options {
			if option == nil {
				continue
			}
			if _, ok := selected[option.ID]; !ok {
				continue
			}
			items = append(items, cloneValue(option.Item))
		}
		return items, true, nil
	default:
		return raw, true, nil
	}
}

func normalizeSelectionList(raw interface{}) map[string]struct{} {
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

func applyGroupRowsReview(args map[string]interface{}, xform map[string]interface{}, payload map[string]interface{}) error {
	rowsField := strings.TrimSpace(stringValue(xform["rowsField"]))
	if rowsField == "" {
		rowsField = "rows"
	}
	selectionField := strings.TrimSpace(stringValue(xform["selectionField"]))
	if selectionField == "" {
		selectionField = "selected"
	}
	groupBy := strings.TrimSpace(stringValue(xform["groupBy"]))
	valueField := strings.TrimSpace(stringValue(xform["valueField"]))
	audienceIDField := strings.TrimSpace(stringValue(xform["audienceIdField"]))
	feature := strings.TrimSpace(stringValue(xform["feature"]))
	writePath := strings.TrimSpace(stringValue(xform["writePath"]))
	intentField := strings.TrimSpace(stringValue(xform["intentField"]))
	if intentField == "" {
		intentField = "intent"
	}
	if groupBy == "" || valueField == "" || audienceIDField == "" || feature == "" || writePath == "" {
		return fmt.Errorf("approval review group_rows requires groupBy, valueField, audienceIdField, feature, and writePath")
	}
	rowsRaw, ok := payload[rowsField].([]interface{})
	if !ok {
		return fmt.Errorf("approval review group_rows expected %q array payload", rowsField)
	}
	selectedRows := make([]map[string]interface{}, 0, len(rowsRaw))
	groupCounts := map[string]int{}
	for _, rowRaw := range rowsRaw {
		row, ok := rowRaw.(map[string]interface{})
		if !ok || row == nil {
			continue
		}
		if !boolValue(row[selectionField]) {
			continue
		}
		groupKey := strings.ToLower(strings.TrimSpace(stringValue(row[groupBy])))
		if groupKey == "" {
			continue
		}
		groupCounts[groupKey]++
		selectedRows = append(selectedRows, row)
	}
	if len(selectedRows) == 0 {
		return fmt.Errorf("approval review group_rows requires at least one selected row")
	}
	targetGroup := ""
	if intentGroupMap, ok := xform["intentGroupMap"].(map[string]interface{}); ok && len(intentGroupMap) > 0 {
		intentValue := strings.ToLower(strings.TrimSpace(stringValue(payload[intentField])))
		if intentValue != "" {
			targetGroup = strings.ToLower(strings.TrimSpace(stringValue(intentGroupMap[intentValue])))
		}
	}
	if targetGroup == "" {
		if len(groupCounts) != 1 {
			return fmt.Errorf("approval review group_rows requires a single selected group when no intentGroupMap resolved it")
		}
		for key := range groupCounts {
			targetGroup = key
		}
	}
	groupConfigRaw, ok := xform["groups"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("approval review group_rows requires groups config")
	}
	groupConfig, ok := groupConfigRaw[targetGroup].(map[string]interface{})
	if !ok {
		return fmt.Errorf("approval review group_rows missing group config for %q", targetGroup)
	}
	mode := strings.TrimSpace(stringValue(groupConfig["mode"]))
	selectorDirection := strings.TrimSpace(stringValue(groupConfig["selectorDirection"]))
	targetField := strings.TrimSpace(stringValue(groupConfig["targetField"]))
	if mode == "" || selectorDirection == "" || targetField == "" {
		return fmt.Errorf("approval review group_rows group %q requires mode, selectorDirection, and targetField", targetGroup)
	}
	values := make([]interface{}, 0, len(selectedRows))
	audienceID := 0
	for _, row := range selectedRows {
		if strings.ToLower(strings.TrimSpace(stringValue(row[groupBy]))) != targetGroup {
			continue
		}
		value := row[valueField]
		if strings.TrimSpace(stringValue(value)) == "" {
			continue
		}
		values = append(values, cloneValue(value))
		if audienceID == 0 {
			audienceID = intValue(row[audienceIDField])
		}
	}
	if len(values) == 0 {
		return fmt.Errorf("approval review group_rows produced no values for group %q", targetGroup)
	}
	recommendation := map[string]interface{}{
		"audience_id":        audienceID,
		"mode":               mode,
		"selector_direction": selectorDirection,
		"target_field":       targetField,
		"proposed_value": map[string]interface{}{
			targetField: map[string]interface{}{
				"clauses": []interface{}{
					map[string]interface{}{
						"feature": feature,
						"values":  values,
					},
				},
			},
		},
	}
	if err := resolver.Assign(args, writePath, recommendation); err != nil {
		return fmt.Errorf("apply approval review group_rows: %w", err)
	}
	delete(args, rowsField)
	delete(args, intentField)
	return nil
}

func stringValue(value interface{}) string {
	return strings.TrimSpace(fmt.Sprintf("%v", value))
}

func boolValue(value interface{}) bool {
	switch actual := value.(type) {
	case bool:
		return actual
	case string:
		return strings.EqualFold(strings.TrimSpace(actual), "true")
	default:
		return false
	}
}

func intValue(value interface{}) int {
	switch actual := value.(type) {
	case int:
		return actual
	case int64:
		return int(actual)
	case float64:
		return int(actual)
	case float32:
		return int(actual)
	case json.Number:
		if v, err := actual.Int64(); err == nil {
			return int(v)
		}
	}
	text := strings.TrimSpace(fmt.Sprintf("%v", value))
	if text == "" {
		return 0
	}
	var out int
	if _, err := fmt.Sscanf(text, "%d", &out); err == nil {
		return out
	}
	return 0
}

func cloneValue(value interface{}) interface{} {
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
