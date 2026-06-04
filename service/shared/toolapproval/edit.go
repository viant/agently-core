package toolapproval

import (
	"encoding/json"
	"fmt"
	"reflect"
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
	case "group_fields":
		return applyGroupFieldsReview(args, review.XForm, payload)
	default:
		return fmt.Errorf("unsupported approval review xform type %q", typeName)
	}
}

func ApplyDecisionPatch(args map[string]interface{}, patch map[string]interface{}) error {
	if len(args) == 0 || len(patch) == 0 {
		return nil
	}
	for key, value := range patch {
		if err := applyDecisionPatchValue(args, strings.TrimSpace(key), value); err != nil {
			return err
		}
	}
	return nil
}

func applyDecisionPatchValue(args map[string]interface{}, path string, value interface{}) error {
	if path == "" {
		return nil
	}
	if nested, ok := value.(map[string]interface{}); ok && len(nested) > 0 {
		for key, item := range nested {
			nextPath := key
			if path != "" {
				nextPath = path + "." + strings.TrimSpace(key)
			}
			if err := applyDecisionPatchValue(args, nextPath, item); err != nil {
				return err
			}
		}
		return nil
	}
	if err := resolver.Assign(args, path, cloneValue(value)); err != nil {
		return fmt.Errorf("apply approval decision patch %s: %w", path, err)
	}
	return nil
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
	feature := strings.TrimSpace(stringValue(xform["feature"]))
	writePath := strings.TrimSpace(stringValue(xform["writePath"]))
	replaceTarget := boolValue(xform["replaceTarget"]) || boolValue(xform["resetTarget"])
	copyFields := stringListValue(xform["copyFields"])
	if groupBy == "" || valueField == "" || feature == "" || writePath == "" {
		return fmt.Errorf("approval review group_rows requires groupBy, valueField, feature, and writePath")
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
	intentField := strings.TrimSpace(stringValue(xform["intentField"]))
	if intentField == "" {
		intentField = "intent"
	}
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
	groupRows := make([]map[string]interface{}, 0, len(selectedRows))
	for _, row := range selectedRows {
		if strings.ToLower(strings.TrimSpace(stringValue(row[groupBy]))) != targetGroup {
			continue
		}
		value := row[valueField]
		if strings.TrimSpace(stringValue(value)) == "" {
			continue
		}
		values = append(values, cloneValue(value))
		groupRows = append(groupRows, row)
	}
	if len(values) == 0 {
		return fmt.Errorf("approval review group_rows produced no values for group %q", targetGroup)
	}
	sourceRecommendation := cloneMapValue(existingMapValue(args[writePath]))
	if sourceRecommendation == nil {
		sourceRecommendation = cloneMapValue(existingMapValue(resolver.Select(writePath, args, nil)))
	}
	var recommendation map[string]interface{}
	if !replaceTarget {
		recommendation = cloneMapValue(sourceRecommendation)
	}
	if recommendation == nil {
		recommendation = map[string]interface{}{}
	}
	recommendation["mode"] = mode
	recommendation["selector_direction"] = selectorDirection
	recommendation["target_field"] = targetField
	recommendation["proposed_value"] = map[string]interface{}{
		targetField: map[string]interface{}{
			"clauses": []interface{}{
				map[string]interface{}{
					"feature": feature,
					"values":  values,
				},
			},
		},
	}
	for _, field := range copyFields {
		field = strings.TrimSpace(field)
		if field == "" || sourceRecommendation == nil {
			continue
		}
		value, ok := sourceRecommendation[field]
		if !ok {
			value = resolver.Select(field, sourceRecommendation, nil)
		}
		if isEmptyApprovalValue(value) {
			continue
		}
		if err := resolver.Assign(recommendation, field, cloneValue(value)); err != nil {
			return fmt.Errorf("apply approval review copy field %s: %w", field, err)
		}
	}
	if err := applyConfiguredAssignments(recommendation, xform["assign"]); err != nil {
		return err
	}
	if err := applyConfiguredRowAssignments(recommendation, xform["assignFromRow"], groupRows); err != nil {
		return err
	}
	if err := resolver.Assign(args, writePath, recommendation); err != nil {
		return fmt.Errorf("apply approval review group_rows: %w", err)
	}
	delete(args, rowsField)
	delete(args, intentField)
	return nil
}

func applyGroupFieldsReview(args map[string]interface{}, xform map[string]interface{}, payload map[string]interface{}) error {
	rowsField := strings.TrimSpace(stringValue(xform["rowsField"]))
	if rowsField == "" {
		rowsField = "rows"
	}
	selectionField := strings.TrimSpace(stringValue(xform["selectionField"]))
	if selectionField == "" {
		selectionField = "selected"
	}
	fieldNameField := strings.TrimSpace(stringValue(xform["fieldNameField"]))
	fieldValueField := strings.TrimSpace(stringValue(xform["fieldValueField"]))
	writePath := strings.TrimSpace(stringValue(xform["writePath"]))
	mode := strings.TrimSpace(stringValue(xform["mode"]))
	selectorDirection := strings.TrimSpace(stringValue(xform["selectorDirection"]))
	targetField := strings.TrimSpace(stringValue(xform["targetField"]))
	replaceTarget := boolValue(xform["replaceTarget"]) || boolValue(xform["resetTarget"])
	copyFields := stringListValue(xform["copyFields"])
	if fieldNameField == "" || fieldValueField == "" || writePath == "" || mode == "" || selectorDirection == "" || targetField == "" {
		return fmt.Errorf("approval review group_fields requires fieldNameField, fieldValueField, writePath, mode, selectorDirection, and targetField")
	}
	rowsRaw, ok := payload[rowsField].([]interface{})
	if !ok {
		return fmt.Errorf("approval review group_fields expected %q array payload", rowsField)
	}
	selectedRows := make([]map[string]interface{}, 0, len(rowsRaw))
	for _, rowRaw := range rowsRaw {
		row, ok := rowRaw.(map[string]interface{})
		if !ok || row == nil {
			continue
		}
		if !boolValue(row[selectionField]) {
			continue
		}
		selectedRows = append(selectedRows, row)
	}
	if len(selectedRows) == 0 {
		return fmt.Errorf("approval review group_fields requires at least one selected row")
	}
	sourceRecommendation := cloneMapValue(existingMapValue(args[writePath]))
	if sourceRecommendation == nil {
		sourceRecommendation = cloneMapValue(existingMapValue(resolver.Select(writePath, args, nil)))
	}
	var recommendation map[string]interface{}
	if !replaceTarget {
		recommendation = cloneMapValue(sourceRecommendation)
	}
	if recommendation == nil {
		recommendation = map[string]interface{}{}
	}
	fields := map[string]interface{}{}
	for _, row := range selectedRows {
		fieldName := strings.TrimSpace(stringValue(row[fieldNameField]))
		if fieldName == "" {
			return fmt.Errorf("approval review group_fields requires %q on every selected row", fieldNameField)
		}
		value, exists := row[fieldValueField]
		if !exists || isUnsetApprovalValue(value) {
			return fmt.Errorf("approval review group_fields requires %q on every selected row", fieldValueField)
		}
		fields[fieldName] = cloneValue(value)
	}
	if len(fields) == 0 {
		return fmt.Errorf("approval review group_fields produced no field values")
	}
	recommendation["mode"] = mode
	recommendation["selector_direction"] = selectorDirection
	recommendation["target_field"] = targetField
	recommendation["proposed_value"] = map[string]interface{}{
		"fields": fields,
	}
	for _, field := range copyFields {
		field = strings.TrimSpace(field)
		if field == "" || sourceRecommendation == nil {
			continue
		}
		value, ok := sourceRecommendation[field]
		if !ok {
			value = resolver.Select(field, sourceRecommendation, nil)
		}
		if isEmptyApprovalValue(value) {
			continue
		}
		if err := resolver.Assign(recommendation, field, cloneValue(value)); err != nil {
			return fmt.Errorf("apply approval review copy field %s: %w", field, err)
		}
	}
	if err := applyConfiguredAssignments(recommendation, xform["assign"]); err != nil {
		return err
	}
	if err := applyConfiguredRowAssignments(recommendation, xform["assignFromRow"], selectedRows); err != nil {
		return err
	}
	if err := resolver.Assign(args, writePath, recommendation); err != nil {
		return fmt.Errorf("apply approval review group_fields: %w", err)
	}
	delete(args, rowsField)
	return nil
}

func applyConfiguredAssignments(target map[string]interface{}, config interface{}) error {
	assignments := map[string]interface{}{}
	collectAssignmentPaths("", config, assignments)
	for path, value := range assignments {
		if strings.TrimSpace(path) == "" {
			continue
		}
		if err := resolver.Assign(target, path, cloneValue(value)); err != nil {
			return fmt.Errorf("apply approval review assignment %s: %w", path, err)
		}
	}
	return nil
}

func applyConfiguredRowAssignments(target map[string]interface{}, config interface{}, rows []map[string]interface{}) error {
	assignments := map[string]string{}
	collectStringAssignmentPaths("", config, assignments)
	for path, sourceField := range assignments {
		if strings.TrimSpace(path) == "" || strings.TrimSpace(sourceField) == "" {
			continue
		}
		value, found, err := consensusRowValue(rows, sourceField)
		if err != nil {
			return fmt.Errorf("apply approval review row assignment %s: %w", path, err)
		}
		if !found || isEmptyApprovalValue(value) {
			continue
		}
		if err := resolver.Assign(target, path, cloneValue(value)); err != nil {
			return fmt.Errorf("apply approval review row assignment %s: %w", path, err)
		}
	}
	return nil
}

func collectAssignmentPaths(prefix string, value interface{}, assignments map[string]interface{}) {
	if assignments == nil || value == nil {
		return
	}
	if nested, ok := value.(map[string]interface{}); ok {
		for key, item := range nested {
			key = strings.TrimSpace(key)
			if key == "" {
				continue
			}
			nextPath := key
			if prefix != "" {
				nextPath = prefix + "." + key
			}
			collectAssignmentPaths(nextPath, item, assignments)
		}
		return
	}
	if prefix == "" {
		return
	}
	assignments[prefix] = value
}

func collectStringAssignmentPaths(prefix string, value interface{}, assignments map[string]string) {
	if assignments == nil || value == nil {
		return
	}
	if nested, ok := value.(map[string]interface{}); ok {
		for key, item := range nested {
			key = strings.TrimSpace(key)
			if key == "" {
				continue
			}
			nextPath := key
			if prefix != "" {
				nextPath = prefix + "." + key
			}
			collectStringAssignmentPaths(nextPath, item, assignments)
		}
		return
	}
	if prefix == "" {
		return
	}
	assignments[prefix] = strings.TrimSpace(stringValue(value))
}

func consensusRowValue(rows []map[string]interface{}, field string) (interface{}, bool, error) {
	if len(rows) == 0 {
		return nil, false, nil
	}
	var candidate interface{}
	found := false
	for _, row := range rows {
		value, ok := rowFieldValue(row, field)
		if !ok || isUnsetApprovalValue(value) {
			return nil, false, fmt.Errorf("requires %q on every selected row", field)
		}
		if !found {
			candidate = value
			found = true
			continue
		}
		if !reflect.DeepEqual(candidate, value) {
			return nil, false, fmt.Errorf("requires a single value for %q across selected rows", field)
		}
	}
	return cloneValue(candidate), found, nil
}

func rowFieldValue(row map[string]interface{}, field string) (interface{}, bool) {
	if row == nil {
		return nil, false
	}
	if value, ok := row[field]; ok {
		return value, true
	}
	value := resolver.Select(field, row, nil)
	if value == nil {
		return nil, false
	}
	return value, true
}

func existingMapValue(value interface{}) map[string]interface{} {
	actual, _ := value.(map[string]interface{})
	return actual
}

func cloneMapValue(value map[string]interface{}) map[string]interface{} {
	if value == nil {
		return nil
	}
	cloned, _ := cloneValue(value).(map[string]interface{})
	if cloned == nil {
		return nil
	}
	return cloned
}

func stringListValue(value interface{}) []string {
	switch actual := value.(type) {
	case []string:
		out := make([]string, 0, len(actual))
		for _, item := range actual {
			item = strings.TrimSpace(item)
			if item != "" {
				out = append(out, item)
			}
		}
		return out
	case []interface{}:
		out := make([]string, 0, len(actual))
		for _, item := range actual {
			text := strings.TrimSpace(fmt.Sprintf("%v", item))
			if text != "" {
				out = append(out, text)
			}
		}
		return out
	case string:
		text := strings.TrimSpace(actual)
		if text == "" {
			return nil
		}
		return []string{text}
	default:
		return nil
	}
}

func isEmptyApprovalValue(value interface{}) bool {
	if value == nil {
		return true
	}
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text) == ""
	}
	return false
}

func isUnsetApprovalValue(value interface{}) bool {
	if value == nil {
		return true
	}
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text) == ""
	}
	return false
}

func stringValue(value interface{}) string {
	if value == nil {
		return ""
	}
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
