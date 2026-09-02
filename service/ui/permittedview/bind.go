package permittedview

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"

	forgetypes "github.com/viant/forge/backend/types"
)

func Bind(window *forgetypes.Window, windowID, conversationID string, parameters map[string]any) (*BoundView, error) {
	return BindResource(window, windowID, conversationID, parameters, nil)
}

// BindResource resolves the generic authorization resource from data already
// returned by an ACL-protected bootstrap datasource.
func BindResource(window *forgetypes.Window, windowID, conversationID string, parameters, resource map[string]any) (*BoundView, error) {
	if window == nil {
		return nil, fmt.Errorf("permitted view: window is required")
	}
	result := &BoundView{
		WindowID: strings.TrimSpace(windowID), ConversationID: strings.TrimSpace(conversationID),
		Window: window, WindowForm: cloneMap(parameters), ResourceData: cloneMap(resource),
	}
	if window.Authorization == nil {
		return result, nil
	}
	spec := window.Authorization
	result.ResourceType = strings.ToLower(strings.TrimSpace(spec.ResourceType))
	if spec.Resource != nil {
		if value := strings.ToLower(strings.TrimSpace(spec.Resource.Type)); value != "" {
			result.ResourceType = value
		}
		source := strings.ToLower(strings.TrimSpace(spec.Resource.ID.Source))
		if source == "" {
			source = "windowform"
		}
		if source != "windowform" && source != "resource" {
			return nil, fmt.Errorf("permitted view: unsupported authorization resource source %q", source)
		}
		selectorRoot := parameters
		if source == "resource" {
			selectorRoot = resource
		}
		value, ok := selectValue(selectorRoot, spec.Resource.ID.Selector)
		if !ok {
			return nil, fmt.Errorf("permitted view: authorization resource selector %q was not resolved", spec.Resource.ID.Selector)
		}
		id, err := intValue(value)
		if err != nil || id <= 0 {
			return nil, fmt.Errorf("permitted view: authorization resource selector %q must resolve a positive integer", spec.Resource.ID.Selector)
		}
		result.ResourceID = id
	}
	return result, nil
}

func ResolveRequest(bound *BoundView) (*Request, error) {
	if bound == nil || bound.Window == nil || bound.Window.Authorization == nil {
		return nil, nil
	}
	spec := bound.Window.Authorization
	if bound.ResourceType == "" {
		return nil, fmt.Errorf("permitted view: authorization resource type is required")
	}
	request := &Request{
		ResourceType:                bound.ResourceType,
		RequestedCapabilities:       append([]string(nil), spec.RequestedCapabilities...),
		RequestedGlobalCapabilities: append([]string(nil), spec.RequestedGlobalCapabilities...),
		IncludePrincipal:            true,
	}
	if bound.ResourceID > 0 {
		request.ResourceIDs = []int{bound.ResourceID}
	}
	return request, nil
}

func selectValue(root any, selector string) (any, bool) {
	current := root
	for _, part := range strings.Split(strings.TrimSpace(selector), ".") {
		if part == "" {
			continue
		}
		switch actual := current.(type) {
		case map[string]any:
			var ok bool
			current, ok = actual[part]
			if !ok {
				return nil, false
			}
		case []any:
			index, err := strconv.Atoi(part)
			if err != nil || index < 0 || index >= len(actual) {
				return nil, false
			}
			current = actual[index]
		case []int:
			index, err := strconv.Atoi(part)
			if err != nil || index < 0 || index >= len(actual) {
				return nil, false
			}
			current = actual[index]
		default:
			value := reflect.ValueOf(current)
			if value.Kind() != reflect.Slice && value.Kind() != reflect.Array {
				return nil, false
			}
			index, err := strconv.Atoi(part)
			if err != nil || index < 0 || index >= value.Len() {
				return nil, false
			}
			current = value.Index(index).Interface()
		}
	}
	return current, true
}

func intValue(value any) (int, error) {
	switch actual := value.(type) {
	case int:
		return actual, nil
	case int64:
		return int(actual), nil
	case float64:
		return int(actual), nil
	case jsonNumber:
		parsed, err := strconv.Atoi(string(actual))
		return parsed, err
	case string:
		return strconv.Atoi(strings.TrimSpace(actual))
	default:
		return 0, fmt.Errorf("unsupported integer value %T", value)
	}
}

type jsonNumber string

func cloneMap(value map[string]any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	result := make(map[string]any, len(value))
	for key, item := range value {
		result[key] = item
	}
	return result
}
