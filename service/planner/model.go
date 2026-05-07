package planner

import (
	"encoding/json"
	"fmt"
	"strings"
)

type Output map[string]any

func Parse(raw string) (Output, error) {
	raw = stripFence(raw)
	var out Output
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		start := strings.Index(raw, "{")
		end := strings.LastIndex(raw, "}")
		if start >= 0 && end > start {
			if err2 := json.Unmarshal([]byte(raw[start:end+1]), &out); err2 == nil {
				return out, nil
			}
		}
		return nil, fmt.Errorf("planner parse: %w", err)
	}
	return out, nil
}

func stripFence(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		if nl := strings.IndexByte(s, '\n'); nl >= 0 {
			s = s[nl+1:]
		}
		if strings.HasSuffix(s, "```") {
			s = s[:len(s)-3]
		}
		s = strings.TrimSpace(s)
	}
	return s
}

func CloneOutput(out Output) map[string]any {
	if len(out) == 0 {
		return nil
	}
	data, err := json.Marshal(out)
	if err != nil {
		result := make(map[string]any, len(out))
		for k, v := range out {
			result[k] = v
		}
		return result
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		fallback := make(map[string]any, len(out))
		for k, v := range out {
			fallback[k] = v
		}
		return fallback
	}
	return result
}

func OutputString(out Output, field string) string {
	if len(out) == 0 {
		return ""
	}
	value, ok := out[strings.TrimSpace(field)]
	if !ok {
		return ""
	}
	switch actual := value.(type) {
	case string:
		return strings.TrimSpace(actual)
	default:
		return strings.TrimSpace(fmt.Sprint(actual))
	}
}

func OutputStringSlice(out Output, field string) []string {
	if len(out) == 0 {
		return nil
	}
	value, ok := out[strings.TrimSpace(field)]
	if !ok {
		return nil
	}
	switch actual := value.(type) {
	case []string:
		result := make([]string, 0, len(actual))
		for _, item := range actual {
			if trimmed := strings.TrimSpace(item); trimmed != "" {
				result = append(result, trimmed)
			}
		}
		return result
	case []any:
		result := make([]string, 0, len(actual))
		for _, item := range actual {
			if trimmed := strings.TrimSpace(fmt.Sprint(item)); trimmed != "" {
				result = append(result, trimmed)
			}
		}
		return result
	default:
		trimmed := strings.TrimSpace(fmt.Sprint(actual))
		if trimmed == "" {
			return nil
		}
		return []string{trimmed}
	}
}

func OutputMap(out Output, field string) map[string]any {
	if len(out) == 0 {
		return nil
	}
	value, ok := out[strings.TrimSpace(field)]
	if !ok {
		return nil
	}
	actual, ok := value.(map[string]any)
	if !ok || len(actual) == 0 {
		return nil
	}
	result := make(map[string]any, len(actual))
	for k, v := range actual {
		result[k] = v
	}
	return result
}

func OutputBoolPtr(out Output, field string) *bool {
	if len(out) == 0 {
		return nil
	}
	value, ok := out[strings.TrimSpace(field)]
	if !ok {
		return nil
	}
	switch actual := value.(type) {
	case bool:
		v := actual
		return &v
	default:
		return nil
	}
}
