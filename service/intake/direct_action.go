package intake

import (
	"fmt"
	"strconv"
	"strings"

	mcpname "github.com/viant/agently-core/pkg/mcpname"
)

func normalizeDirectActionWithScope(action DirectActionContext, scope map[string]string) DirectActionContext {
	action.ToolName = strings.TrimSpace(action.ToolName)
	action.InputJSON = strings.TrimSpace(action.InputJSON)
	action.AssistantText = strings.TrimSpace(action.AssistantText)
	if action.ToolName == "" || action.AssistantText == "" || len(action.Input) == 0 {
		return DirectActionContext{}
	}
	if !validDirectActionInput(action) {
		return DirectActionContext{}
	}
	return action
}

func NormalizeClassifierDirectAction(action *DirectActionContext) DirectActionContext {
	if action == nil {
		return DirectActionContext{}
	}
	return normalizeDirectActionWithScope(*action, nil)
}

func normalizeDirectActionWithContext(action DirectActionContext, scope map[string]string, hints ...string) DirectActionContext {
	return normalizeDirectActionWithScope(action, scope)
}

func normalizeDirectActionValue(value interface{}) ([]interface{}, bool) {
	switch actual := value.(type) {
	case nil:
		return nil, false
	case []interface{}:
		result := make([]interface{}, 0, len(actual))
		for _, item := range actual {
			normalized, ok := normalizeScalarDirectActionValue(item)
			if !ok {
				continue
			}
			result = append(result, normalized)
		}
		if len(result) == 0 {
			return nil, false
		}
		return result, true
	case []string:
		result := make([]interface{}, 0, len(actual))
		for _, item := range actual {
			normalized, ok := normalizeScalarDirectActionValue(item)
			if !ok {
				continue
			}
			result = append(result, normalized)
		}
		if len(result) == 0 {
			return nil, false
		}
		return result, true
	default:
		normalized, ok := normalizeScalarDirectActionValue(actual)
		if !ok {
			return nil, false
		}
		return []interface{}{normalized}, true
	}
}

func normalizeScalarDirectActionValue(value interface{}) (interface{}, bool) {
	switch actual := value.(type) {
	case nil:
		return nil, false
	case string:
		trimmed := strings.TrimSpace(actual)
		if trimmed == "" {
			return nil, false
		}
		if parsed, err := strconv.Atoi(trimmed); err == nil {
			return parsed, true
		}
		return trimmed, true
	case float64:
		return int(actual), true
	case float32:
		return int(actual), true
	case int, int8, int16, int32, int64:
		return value, true
	case uint, uint8, uint16, uint32, uint64:
		return fmt.Sprintf("%v", value), true
	default:
		trimmed := strings.TrimSpace(fmt.Sprint(value))
		if trimmed == "" || trimmed == "<nil>" {
			return nil, false
		}
		if parsed, err := strconv.Atoi(trimmed); err == nil {
			return parsed, true
		}
		return trimmed, true
	}
}

func normalizeInterfaceMap(value interface{}) (map[string]interface{}, bool) {
	mapped, ok := value.(map[string]interface{})
	if !ok || len(mapped) == 0 {
		return nil, false
	}
	return mapped, true
}

func validDirectActionInput(action DirectActionContext) bool {
	toolName := strings.ToLower(strings.TrimSpace(mcpname.Display(action.ToolName)))
	switch toolName {
	case "ui/view/open":
		return strings.TrimSpace(directActionString(action.Input["id"])) != ""
	default:
		return true
	}
}

func directActionString(value interface{}) string {
	switch actual := value.(type) {
	case nil:
		return ""
	case string:
		return actual
	default:
		return strings.TrimSpace(fmt.Sprint(actual))
	}
}
