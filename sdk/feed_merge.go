package sdk

import (
	"encoding/json"
	"strings"
)

// mergeFeedPayloads merges multiple tool call response payloads into a single
// root object with "input" and "output" keys. This supports feeds that match
// multiple tool calls (e.g., explorer matching resources/roots, resources/list, etc.)
func mergeFeedPayloads(payloads []string) map[string]interface{} {
	var mergedOutput interface{}
	for _, payload := range payloads {
		payload = strings.TrimSpace(payload)
		if payload == "" {
			continue
		}
		var parsed interface{}
		if err := json.Unmarshal([]byte(payload), &parsed); err != nil {
			continue
		}
		mergedOutput = mergeJSONLike(mergedOutput, parsed)
	}
	if mergedOutput == nil {
		mergedOutput = map[string]interface{}{}
	}
	return map[string]interface{}{
		"output": mergedOutput,
	}
}

// mergeJSONLike recursively merges two JSON-like values.
// Maps are merged key-by-key. Slices are appended. Scalars: b wins.
func mergeJSONLike(a, b interface{}) interface{} {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	aMap, aOK := toMap(a)
	bMap, bOK := toMap(b)
	if aOK && bOK {
		for k, v := range bMap {
			if existing, ok := aMap[k]; ok {
				aMap[k] = mergeJSONLike(existing, v)
			} else {
				aMap[k] = v
			}
		}
		return aMap
	}
	aSlice, aIsSlice := toSlice(a)
	bSlice, bIsSlice := toSlice(b)
	if aIsSlice && bIsSlice {
		return append(aSlice, bSlice...)
	}
	if aIsSlice {
		return append(aSlice, b)
	}
	if bIsSlice {
		return append([]interface{}{a}, bSlice...)
	}
	return b
}

func toMap(v interface{}) (map[string]interface{}, bool) {
	if m, ok := v.(map[string]interface{}); ok {
		return m, true
	}
	return nil, false
}

func toSlice(v interface{}) ([]interface{}, bool) {
	if s, ok := v.([]interface{}); ok {
		return s, true
	}
	return nil, false
}

// enrichExplorerData derives entries from merged output for the explorer feed.
func enrichExplorerData(rootData map[string]interface{}) {
	output, _ := rootData["output"].(map[string]interface{})
	if output == nil {
		return
	}
	var entries []interface{}
	// Collect from various output formats.
	if items, ok := output["items"].([]interface{}); ok {
		entries = append(entries, items...)
	}
	if files, ok := output["files"].([]interface{}); ok {
		entries = append(entries, files...)
	}
	if roots, ok := output["roots"].([]interface{}); ok {
		entries = append(entries, roots...)
	}
	if roots, ok := output["Roots"].([]interface{}); ok {
		entries = append(entries, roots...)
	}
	if len(entries) > 0 {
		rootData["entries"] = entries
	}
}

// enrichPlanData adds a "content" field to each plan step.
func enrichPlanData(rootData map[string]interface{}) {
	output, _ := rootData["output"].(map[string]interface{})
	if output == nil {
		return
	}
	plan, ok := output["plan"].([]interface{})
	if !ok {
		plan, ok = output["Plan"].([]interface{})
	}
	if !ok || len(plan) == 0 {
		return
	}
	for _, item := range plan {
		stepMap, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		status := stringVal(stepMap, "status", "Status")
		step := stringVal(stepMap, "step", "Step")
		stepMap["content"] = "**Status:** " + status + "\n\n**Step:** " + step
	}
}

func stringVal(m map[string]interface{}, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				return strings.TrimSpace(s)
			}
		}
	}
	return ""
}
