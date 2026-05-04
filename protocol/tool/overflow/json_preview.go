package overflow

import (
	"encoding/json"
	"fmt"
	"strings"
)

// BuildJSONContinuationPreview truncates the largest string field inside a JSON
// object while preserving or synthesizing a machine-readable continuation
// contract for message-show. It returns ok=false when body is not JSON or when
// no truncation is needed.
func BuildJSONContinuationPreview(body, refMessageID string, threshold int) (string, bool) {
	body = strings.TrimSpace(body)
	if !strings.HasPrefix(body, "{") || threshold <= 0 {
		return "", false
	}

	var root interface{}
	if err := json.Unmarshal([]byte(body), &root); err != nil {
		return "", false
	}
	parent, key, idx, value := findLargestString(root)
	if parent == nil || value == "" {
		return "", false
	}
	limit := int(0.9 * float64(threshold))
	if limit <= 0 {
		limit = threshold
	}
	if limit <= 0 || len(value) <= limit {
		return "", false
	}

	truncated := strings.TrimSpace(value[:limit])
	switch container := parent.(type) {
	case map[string]interface{}:
		container[key] = truncated
	case []interface{}:
		if idx < 0 || idx >= len(container) {
			return "", false
		}
		container[idx] = truncated
	default:
		return "", false
	}

	rootMap, ok := root.(map[string]interface{})
	if !ok {
		return "", false
	}

	sourceMessageID := strings.TrimSpace(refMessageID)
	if existing, ok := rootMap["messageId"].(string); ok && strings.TrimSpace(existing) != "" {
		sourceMessageID = strings.TrimSpace(existing)
	}

	remaining := len(value) - len(truncated)
	returned := len(truncated)
	offset, length, hasNativeRange := nextByteRange(rootMap, returned, remaining)
	if !hasNativeRange {
		length = returned
		if remaining > 0 && remaining < length {
			length = remaining
		}
	}

	cont := ensureMapField(rootMap, "continuation")
	cont["hasMore"] = true
	cont["remaining"] = remaining
	cont["returned"] = returned
	if _, ok := cont["mode"]; !ok {
		cont["mode"] = "head"
	}

	nextRange := ensureMapField(cont, "nextRange")
	nextRange["bytes"] = map[string]interface{}{
		"offset": offset,
		"length": length,
	}

	if sourceMessageID != "" {
		rootMap["messageId"] = sourceMessageID
	}
	if _, ok := rootMap["continuationHint"]; !ok && sourceMessageID != "" {
		rootMap["continuationHint"] = fmt.Sprintf(
			"Call message-show with messageId=%s and byteRange.from=%d, byteRange.to=%d.",
			sourceMessageID,
			offset,
			offset+length,
		)
	}
	if _, ok := rootMap["nextArgs"]; !ok {
		nextArgs := map[string]interface{}{}
		if sourceMessageID != "" {
			nextArgs["messageId"] = sourceMessageID
			nextArgs["byteRange"] = map[string]interface{}{
				"from": offset,
				"to":   offset + length,
			}
		} else {
			nextArgs["offsetBytes"] = offset
			nextArgs["lengthBytes"] = length
			nextArgs["maxBytes"] = length
		}
		rootMap["nextArgs"] = nextArgs
	}

	out, err := json.Marshal(rootMap)
	if err != nil {
		return "", false
	}
	return string(out), true
}

func nextByteRange(root map[string]interface{}, fallbackOffset, fallbackRemaining int) (offset, length int, ok bool) {
	cont, _ := root["continuation"].(map[string]interface{})
	if cont == nil {
		return fallbackOffset, fallbackRemaining, false
	}
	nextRange, _ := cont["nextRange"].(map[string]interface{})
	if nextRange == nil {
		return fallbackOffset, fallbackRemaining, false
	}
	bytesHint, _ := nextRange["bytes"].(map[string]interface{})
	if bytesHint == nil {
		return fallbackOffset, fallbackRemaining, false
	}
	offset = intFromAny(bytesHint["offset"])
	if offset == 0 {
		offset = intFromAny(bytesHint["offsetBytes"])
	}
	length = intFromAny(bytesHint["length"])
	if length == 0 {
		length = intFromAny(bytesHint["lengthBytes"])
	}
	if offset <= 0 || length <= 0 {
		return fallbackOffset, fallbackRemaining, false
	}
	return offset, length, true
}

func ensureMapField(root map[string]interface{}, key string) map[string]interface{} {
	if existing, ok := root[key].(map[string]interface{}); ok && existing != nil {
		return existing
	}
	out := map[string]interface{}{}
	root[key] = out
	return out
}

func findLargestString(v interface{}) (parent interface{}, key string, idx int, val string) {
	idx = -1
	var visit func(node interface{}, p interface{}, k string, i int)
	visit = func(node interface{}, p interface{}, k string, i int) {
		switch n := node.(type) {
		case string:
			if len(n) > len(val) {
				parent, key, idx, val = p, k, i, n
			}
		case map[string]interface{}:
			for mk, mv := range n {
				visit(mv, n, mk, -1)
			}
		case []interface{}:
			for si, sv := range n {
				visit(sv, n, "", si)
			}
		}
	}
	visit(v, nil, "", -1)
	return
}

func intFromAny(v interface{}) int {
	switch t := v.(type) {
	case int:
		return t
	case int64:
		return int(t)
	case float64:
		return int(t)
	case json.Number:
		if i, err := t.Int64(); err == nil {
			return int(i)
		}
		if f, err := t.Float64(); err == nil {
			return int(f)
		}
	}
	return 0
}
