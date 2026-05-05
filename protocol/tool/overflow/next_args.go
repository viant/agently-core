package overflow

import (
	"encoding/json"
	"strings"

	yaml "gopkg.in/yaml.v3"
)

// ExtractMessageShowNextArgs returns canonical message-show nextArgs when the
// provided tool result explicitly advertises unresolved continuation.
func ExtractMessageShowNextArgs(body string) (map[string]interface{}, bool) {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return nil, false
	}
	if args, ok := extractMessageShowNextArgsJSON(trimmed); ok {
		return args, true
	}
	return extractMessageShowNextArgsYAML(trimmed)
}

func extractMessageShowNextArgsJSON(body string) (map[string]interface{}, bool) {
	if !strings.HasPrefix(body, "{") {
		return nil, false
	}
	root := map[string]interface{}{}
	if err := json.Unmarshal([]byte(body), &root); err != nil {
		return nil, false
	}
	nextArgs, _ := root["nextArgs"].(map[string]interface{})
	if len(nextArgs) == 0 {
		return nil, false
	}
	if continuationHasMore(root) || strings.EqualFold(strings.TrimSpace(continuationStringField(root, "useToolToSeeMore")), "message-show") {
		return cloneMap(nextArgs), true
	}
	return nil, false
}

func extractMessageShowNextArgsYAML(body string) (map[string]interface{}, bool) {
	root := map[string]interface{}{}
	if err := yaml.Unmarshal([]byte(body), &root); err != nil {
		return nil, false
	}
	nextArgs, _ := root["nextArgs"].(map[string]interface{})
	if len(nextArgs) == 0 {
		return nil, false
	}
	if boolField(root, "hasMore") || boolField(root, "overflow") || strings.EqualFold(strings.TrimSpace(continuationStringField(root, "useToolToSeeMore")), "message-show") {
		return cloneMap(nextArgs), true
	}
	return nil, false
}

func continuationHasMore(root map[string]interface{}) bool {
	if boolField(root, "hasMore") {
		return true
	}
	cont, _ := root["continuation"].(map[string]interface{})
	return boolField(cont, "hasMore")
}

func boolField(root map[string]interface{}, key string) bool {
	if root == nil {
		return false
	}
	value, ok := root[key]
	if !ok {
		return false
	}
	actual, ok := value.(bool)
	return ok && actual
}

func continuationStringField(root map[string]interface{}, key string) string {
	if root == nil {
		return ""
	}
	value, ok := root[key]
	if !ok || value == nil {
		return ""
	}
	text, _ := value.(string)
	return text
}

func cloneMap(src map[string]interface{}) map[string]interface{} {
	if len(src) == 0 {
		return nil
	}
	result := make(map[string]interface{}, len(src))
	for key, value := range src {
		result[key] = cloneValue(value)
	}
	return result
}

func cloneSlice(src []interface{}) []interface{} {
	if len(src) == 0 {
		return nil
	}
	result := make([]interface{}, len(src))
	for i, value := range src {
		result[i] = cloneValue(value)
	}
	return result
}

func cloneValue(value interface{}) interface{} {
	switch actual := value.(type) {
	case map[string]interface{}:
		return cloneMap(actual)
	case []interface{}:
		return cloneSlice(actual)
	default:
		return actual
	}
}
