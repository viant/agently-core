package async

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

type Extracted struct {
	Status  string
	Message string
	Percent *int
	KeyData json.RawMessage
	Error   string
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
