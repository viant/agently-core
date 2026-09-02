package permittedview

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"

	forgetypes "github.com/viant/forge/backend/types"
)

type reducedCondition struct {
	constant *bool
	dynamic  any
}

func Compile(bound *BoundView, snapshot *Snapshot) (*Result, error) {
	if bound == nil || bound.Window == nil {
		return nil, fmt.Errorf("permitted view: bound window is required")
	}
	if bound.Window.Authorization == nil {
		return &Result{Window: bound.Window, DataSourceRefs: collectWindowDataSourceRefs(bound.Window)}, nil
	}
	resource := (*Resource)(nil)
	if bound.ResourceID > 0 && snapshot != nil {
		resource = snapshot.Resources[fmt.Sprint(bound.ResourceID)]
	}
	if strings.EqualFold(bound.Window.Authorization.Scope, "resource") && (resource == nil || resource.Capabilities["read"] != true) {
		return &Result{Authorization: snapshot, Resource: resource, DataSourceRefs: map[string]bool{}, Denied: true,
			Diagnostics: []Diagnostic{{Code: "resource_read_denied", Message: "resource read capability was not granted"}}}, nil
	}
	authorization := authorizationScope(snapshot, resource)
	raw, err := json.Marshal(bound.Window)
	if err != nil {
		return nil, err
	}
	var tree map[string]any
	if err = json.Unmarshal(raw, &tree); err != nil {
		return nil, err
	}
	compiled, keep := compileNode(tree, authorization)
	if !keep {
		return &Result{Authorization: snapshot, Resource: resource, DataSourceRefs: map[string]bool{}, Denied: true}, nil
	}
	compiledMap := compiled.(map[string]any)
	refs := collectRefs(compiledMap)
	if dataSources, ok := compiledMap["dataSource"].(map[string]any); ok {
		for key := range dataSources {
			if !refs[key] {
				delete(dataSources, key)
			}
		}
	}
	compiledJSON, _ := json.Marshal(compiledMap)
	window := &forgetypes.Window{}
	if err = json.Unmarshal(compiledJSON, window); err != nil {
		return nil, err
	}
	result := &Result{Window: window, Authorization: snapshot, Resource: resource, DataSourceRefs: refs}
	if snapshot != nil {
		result.ExpiresAt = snapshot.ExpiresAt
	}
	return result, nil
}

func authorizationScope(snapshot *Snapshot, resource *Resource) map[string]any {
	result := map[string]any{"principal": map[string]any{}, "account": map[string]any{}, "globalCapabilities": map[string]any{}, "resource": nil}
	if snapshot == nil {
		return result
	}
	raw, _ := json.Marshal(map[string]any{
		"authorizationVersion": snapshot.AuthorizationVersion,
		"expiresAt":            snapshot.ExpiresAt,
		"principal":            snapshot.Principal,
		"account":              snapshot.Account,
		"globalCapabilities":   snapshot.GlobalCapabilities,
		"resource":             resource,
	})
	_ = json.Unmarshal(raw, &result)
	return result
}

func compileNode(value any, authorization map[string]any) (any, bool) {
	switch actual := value.(type) {
	case []any:
		result := make([]any, 0, len(actual))
		for _, item := range actual {
			compiled, keep := compileNode(item, authorization)
			if keep {
				result = append(result, compiled)
			}
		}
		return result, true
	case map[string]any:
		node := make(map[string]any, len(actual))
		for key, item := range actual {
			node[key] = item
		}
		if remove, err := applyGuard(node, "visibleWhen", authorization, "visible"); err != nil || remove {
			return nil, false
		}
		if remove, err := applyGuard(node, "hiddenWhen", authorization, "hidden"); err != nil || remove {
			return nil, false
		}
		_, _ = applyGuard(node, "disabledWhen", authorization, "disabled")
		_, _ = applyGuard(node, "readOnlyWhen", authorization, "readOnly")
		for key, item := range node {
			if key == "visibleWhen" || key == "hiddenWhen" || key == "disabledWhen" || key == "readOnlyWhen" {
				continue
			}
			compiled, keep := compileNode(item, authorization)
			if !keep {
				delete(node, key)
			} else {
				node[key] = compiled
			}
		}
		return node, true
	default:
		return value, true
	}
}

func applyGuard(node map[string]any, key string, authorization map[string]any, mode string) (bool, error) {
	condition, ok := node[key]
	if !ok {
		return false, nil
	}
	reduced := reduceCondition(condition, authorization)
	if reduced.constant == nil {
		node[key] = reduced.dynamic
		return false, nil
	}
	delete(node, key)
	value := *reduced.constant
	switch mode {
	case "visible":
		return !value, nil
	case "hidden":
		return value, nil
	case "disabled":
		if value {
			node["disabled"] = true
		}
	case "readOnly":
		if value {
			node["readOnly"] = true
		}
	}
	return false, nil
}

func reduceCondition(value any, authorization map[string]any) reducedCondition {
	condition, ok := value.(map[string]any)
	if !ok {
		return constant(false)
	}
	if entries, ok := condition["all"].([]any); ok {
		dynamic := []any{}
		for _, entry := range entries {
			reduced := reduceCondition(entry, authorization)
			if reduced.constant != nil && !*reduced.constant {
				return constant(false)
			}
			if reduced.constant == nil {
				dynamic = append(dynamic, reduced.dynamic)
			}
		}
		if len(dynamic) == 0 {
			return constant(true)
		}
		if len(dynamic) == 1 {
			return reducedCondition{dynamic: dynamic[0]}
		}
		return reducedCondition{dynamic: map[string]any{"all": dynamic}}
	}
	if entries, ok := condition["any"].([]any); ok {
		dynamic := []any{}
		for _, entry := range entries {
			reduced := reduceCondition(entry, authorization)
			if reduced.constant != nil && *reduced.constant {
				return constant(true)
			}
			if reduced.constant == nil {
				dynamic = append(dynamic, reduced.dynamic)
			}
		}
		if len(dynamic) == 0 {
			return constant(false)
		}
		if len(dynamic) == 1 {
			return reducedCondition{dynamic: dynamic[0]}
		}
		return reducedCondition{dynamic: map[string]any{"any": dynamic}}
	}
	if nested, ok := condition["not"]; ok {
		reduced := reduceCondition(nested, authorization)
		if reduced.constant != nil {
			return constant(!*reduced.constant)
		}
		return reducedCondition{dynamic: map[string]any{"not": reduced.dynamic}}
	}
	if strings.EqualFold(stringValue(condition["source"]), "authorization") {
		return constant(evaluateAuthorizationLeaf(condition, authorization))
	}
	return reducedCondition{dynamic: condition}
}

func evaluateAuthorizationLeaf(condition, authorization map[string]any) bool {
	field := stringValue(condition["field"])
	if field == "" {
		field = stringValue(condition["selector"])
	}
	actual, _ := selectValue(authorization, field)
	if expected, ok := condition["equals"]; ok {
		return reflect.DeepEqual(actual, expected)
	}
	if expected, ok := condition["notEquals"]; ok {
		return !reflect.DeepEqual(actual, expected)
	}
	if values, ok := condition["in"].([]any); ok {
		for _, expected := range values {
			if reflect.DeepEqual(actual, expected) {
				return true
			}
		}
		return false
	}
	if expected, ok := condition["contains"]; ok {
		return containsValue(actual, expected)
	}
	if expected, ok := condition["empty"].(bool); ok {
		return isEmpty(actual) == expected
	}
	if expected, ok := condition["notEmpty"].(bool); ok {
		return (!isEmpty(actual)) == expected
	}
	if expected, ok := condition["exists"].(bool); ok {
		return (actual != nil) == expected
	}
	return false
}

func containsValue(actual, expected any) bool {
	value := reflect.ValueOf(actual)
	if !value.IsValid() {
		return false
	}
	switch value.Kind() {
	case reflect.Slice, reflect.Array:
		for i := 0; i < value.Len(); i++ {
			if reflect.DeepEqual(value.Index(i).Interface(), expected) {
				return true
			}
		}
	case reflect.String:
		return strings.Contains(value.String(), fmt.Sprint(expected))
	case reflect.Map:
		for _, key := range value.MapKeys() {
			if fmt.Sprint(key.Interface()) == fmt.Sprint(expected) {
				return true
			}
		}
	}
	return false
}

func isEmpty(value any) bool {
	if value == nil {
		return true
	}
	ref := reflect.ValueOf(value)
	switch ref.Kind() {
	case reflect.String, reflect.Array, reflect.Slice, reflect.Map:
		return ref.Len() == 0
	}
	return false
}

func constant(value bool) reducedCondition { return reducedCondition{constant: &value} }
func stringValue(value any) string         { return strings.TrimSpace(fmt.Sprint(value)) }

func collectWindowDataSourceRefs(window *forgetypes.Window) map[string]bool {
	raw, _ := json.Marshal(window)
	var tree map[string]any
	_ = json.Unmarshal(raw, &tree)
	return collectRefs(tree)
}

func collectRefs(value any) map[string]bool {
	result := map[string]bool{}
	var visit func(any, string)
	visit = func(current any, key string) {
		switch actual := current.(type) {
		case map[string]any:
			for childKey, child := range actual {
				if strings.EqualFold(childKey, "authorization") || strings.EqualFold(childKey, "dataSource") {
					continue
				}
				if strings.EqualFold(childKey, "dataSourceRefs") {
					if refs, ok := child.(map[string]any); ok {
						for _, ref := range refs {
							if name := stringValue(ref); name != "" {
								result[name] = true
							}
						}
					}
				}
				visit(child, childKey)
			}
		case []any:
			for _, child := range actual {
				visit(child, key)
			}
		case string:
			if strings.HasSuffix(strings.ToLower(key), "datasourceref") && strings.TrimSpace(actual) != "" {
				result[strings.TrimSpace(actual)] = true
			}
		}
	}
	visit(value, "")
	return result
}

func sortedKeys(values map[string]bool) []string {
	result := make([]string, 0, len(values))
	for key := range values {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}
