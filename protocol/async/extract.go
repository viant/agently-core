package async

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

type Extracted struct {
	Status      string
	Message     string
	MessageKind string
	Percent     *int
	KeyData     json.RawMessage
	Error       string
}

func ExtractOperationID(raw string, path string) (string, error) {
	root, err := decodeRoot(raw)
	if err != nil {
		return "", err
	}
	value, ok := lookup(root, path)
	if !ok || value == nil {
		return "", nil
	}
	return strings.TrimSpace(fmt.Sprint(value)), nil
}

func ExtractPayload(raw string, selector Selector) (*Extracted, error) {
	root, err := decodeRoot(raw)
	if err != nil {
		return nil, err
	}
	result := &Extracted{}
	if selector.StatusPath != "" {
		if value, ok := lookup(root, selector.StatusPath); ok && value != nil {
			result.Status = strings.TrimSpace(fmt.Sprint(value))
		}
	}
	if selector.ProgressPath != "" {
		if value, ok := lookup(root, selector.ProgressPath); ok && value != nil {
			switch actual := value.(type) {
			case map[string]interface{}:
				if pct, ok := intFromAny(actual["percent"]); ok {
					result.Percent = &pct
				}
				if msg, ok := actual["message"]; ok {
					result.Message = strings.TrimSpace(fmt.Sprint(msg))
				}
			default:
				if pct, ok := intFromAny(actual); ok {
					result.Percent = &pct
				}
			}
		}
	}
	if result.Message == "" && selector.MessagePath != "" {
		if value, ok := lookup(root, selector.MessagePath); ok && value != nil {
			result.Message = strings.TrimSpace(fmt.Sprint(value))
		}
	}
	if value, ok := lookup(root, "messageKind"); ok && value != nil {
		result.MessageKind = strings.TrimSpace(fmt.Sprint(value))
	}
	if selector.DataPath != "" {
		if value, ok := lookup(root, selector.DataPath); ok && value != nil {
			if data, err := json.Marshal(value); err == nil {
				result.KeyData = data
			}
		}
	}
	if selector.ErrorPath != "" {
		if value, ok := lookup(root, selector.ErrorPath); ok && value != nil {
			result.Error = strings.TrimSpace(fmt.Sprint(value))
		}
	}
	return result, nil
}

func ExtractIntent(args map[string]any, path string, fallback string) string {
	fallback = strings.TrimSpace(fallback)
	if len(args) == 0 {
		return fallback
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return fallback
	}
	value, ok := lookup(args, path)
	if !ok {
		return fallback
	}
	text := intentValueToString(value)
	if text == "" {
		return fallback
	}
	return normalizeIntentText(text)
}

func ExtractSummary(args map[string]any, paths []string) string {
	if len(args) == 0 {
		return ""
	}
	if summary := extractSummaryFromPaths(args, paths); summary != "" {
		return summary
	}
	return extractTopLevelSummary(args)
}

func decodeRoot(raw string) (interface{}, error) {
	var root interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &root); err != nil {
		return nil, err
	}
	return root, nil
}

func lookup(root interface{}, path string) (interface{}, bool) {
	path = strings.TrimSpace(path)
	if path == "" {
		return root, true
	}
	current := root
	for _, part := range strings.Split(path, ".") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		switch actual := current.(type) {
		case map[string]interface{}:
			next, ok := actual[part]
			if !ok {
				return nil, false
			}
			current = next
		case []interface{}:
			index, err := strconv.Atoi(part)
			if err != nil || index < 0 || index >= len(actual) {
				return nil, false
			}
			current = actual[index]
		default:
			return nil, false
		}
	}
	return current, true
}

func intentValueToString(value interface{}) string {
	switch actual := value.(type) {
	case nil:
		return ""
	case string:
		return actual
	case fmt.Stringer:
		return actual.String()
	case bool:
		return fmt.Sprint(actual)
	case int, int8, int16, int32, int64:
		return fmt.Sprint(actual)
	case uint, uint8, uint16, uint32, uint64:
		return fmt.Sprint(actual)
	case float32:
		if math.IsNaN(float64(actual)) || math.IsInf(float64(actual), 0) {
			return ""
		}
		return fmt.Sprint(actual)
	case float64:
		if math.IsNaN(actual) || math.IsInf(actual, 0) {
			return ""
		}
		return fmt.Sprint(actual)
	case json.Number:
		return actual.String()
	case []interface{}, map[string]interface{}:
		return ""
	default:
		return ""
	}
}

func normalizeIntentText(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	text = strings.Join(strings.Fields(text), " ")
	return truncateRunes(text, 200)
}

func extractSummaryFromPaths(args map[string]any, paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	parts := make([]string, 0, len(paths))
	for _, rawPath := range paths {
		path := strings.TrimSpace(rawPath)
		if path == "" {
			continue
		}
		value, ok := lookup(args, path)
		if !ok {
			continue
		}
		text := normalizeIntentText(intentValueToString(value))
		if text == "" {
			continue
		}
		label := summaryLabel(path)
		if label == "" {
			parts = append(parts, text)
			continue
		}
		parts = append(parts, label+"="+text)
		if len(parts) >= 6 {
			break
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return truncateRunes(strings.Join(parts, " | "), 240)
}

func extractTopLevelSummary(args map[string]any) string {
	keys := make([]string, 0, len(args))
	for key := range args {
		key = strings.TrimSpace(key)
		if key == "" || key == "_agently" {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		text := normalizeIntentText(intentValueToString(args[key]))
		if text == "" {
			continue
		}
		parts = append(parts, key+"="+text)
		if len(parts) >= 6 {
			break
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return truncateRunes(strings.Join(parts, " | "), 240)
}

func summaryLabel(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	parts := strings.Split(path, ".")
	if len(parts) == 0 {
		return ""
	}
	return strings.TrimSpace(parts[len(parts)-1])
}

func truncateRunes(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	runes := []rune(text)
	return strings.TrimSpace(string(runes[:limit]))
}

func intFromAny(value interface{}) (int, bool) {
	switch actual := value.(type) {
	case int:
		return actual, true
	case int64:
		return int(actual), true
	case float64:
		return int(actual), true
	case json.Number:
		if i, err := actual.Int64(); err == nil {
			return int(i), true
		}
	case string:
		if i, err := strconv.Atoi(strings.TrimSpace(actual)); err == nil {
			return i, true
		}
	}
	return 0, false
}
