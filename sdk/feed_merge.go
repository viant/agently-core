package sdk

import (
	"encoding/json"
	"strings"
)

func buildFeedData(feedID string, requestPayloads, responsePayloads []string) map[string]interface{} {
	requests := parsePayloadList(requestPayloads)
	responses := parsePayloadList(responsePayloads)

	rootData := map[string]interface{}{
		"input":  lastPayloadObject(requests),
		"output": map[string]interface{}{},
	}
	switch strings.ToLower(strings.TrimSpace(feedID)) {
	case "explorer":
		rootData["output"] = buildExplorerOutput(responses)
		enrichExplorerData(rootData)
	case "terminal":
		rootData["output"] = buildTerminalOutput(responses)
	case "changes":
		rootData["output"] = buildLastOutput(responses)
	case "plan":
		rootData["output"] = buildLastOutput(responses)
		enrichPlanData(rootData)
	default:
		rootData["output"] = buildLastOutput(responses)
	}
	return rootData
}

func parsePayloadList(payloads []string) []interface{} {
	var result []interface{}
	for _, payload := range payloads {
		payload = strings.TrimSpace(payload)
		if payload == "" {
			continue
		}
		var parsed interface{}
		if err := json.Unmarshal([]byte(payload), &parsed); err != nil {
			continue
		}
		result = append(result, parsed)
	}
	return result
}

func lastPayloadObject(values []interface{}) map[string]interface{} {
	for i := len(values) - 1; i >= 0; i-- {
		if obj, ok := values[i].(map[string]interface{}); ok {
			return cloneMap(obj)
		}
	}
	return map[string]interface{}{}
}

func buildLastOutput(values []interface{}) map[string]interface{} {
	return lastPayloadObject(values)
}

func buildExplorerOutput(values []interface{}) map[string]interface{} {
	output := map[string]interface{}{}
	var files []interface{}
	var items []interface{}
	var roots []interface{}
	for _, raw := range values {
		obj, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		if list, ok := obj["files"].([]interface{}); ok {
			files = append(files, list...)
		}
		if list, ok := obj["items"].([]interface{}); ok {
			items = append(items, list...)
		}
		if list, ok := obj["roots"].([]interface{}); ok {
			roots = append(roots, list...)
		}
		if list, ok := obj["Roots"].([]interface{}); ok {
			roots = append(roots, list...)
		}
		if _, ok := output["stats"]; !ok && obj["stats"] != nil {
			output["stats"] = obj["stats"]
		}
		if _, ok := output["path"]; !ok && obj["path"] != nil {
			output["path"] = obj["path"]
		}
		if _, ok := output["modeApplied"]; !ok && obj["modeApplied"] != nil {
			output["modeApplied"] = obj["modeApplied"]
		}
	}
	if len(files) > 0 {
		output["files"] = files
	}
	if len(items) > 0 {
		output["items"] = items
	}
	if len(roots) > 0 {
		output["roots"] = roots
	}
	return output
}

func buildTerminalOutput(values []interface{}) map[string]interface{} {
	output := map[string]interface{}{}
	var commands []interface{}
	for _, raw := range values {
		obj, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		if list, ok := obj["commands"].([]interface{}); ok {
			commands = append(commands, list...)
		}
		for _, key := range []string{"stdout", "stderr", "status", "error"} {
			if _, exists := output[key]; !exists && obj[key] != nil {
				output[key] = obj[key]
			}
		}
	}
	if len(commands) > 0 {
		output["commands"] = commands
	}
	return output
}

func cloneMap(src map[string]interface{}) map[string]interface{} {
	if src == nil {
		return map[string]interface{}{}
	}
	out := make(map[string]interface{}, len(src))
	for key, value := range src {
		out[key] = value
	}
	return out
}

// enrichExplorerData derives entries from merged output for the explorer feed.
func enrichExplorerData(rootData map[string]interface{}) {
	output, _ := rootData["output"].(map[string]interface{})
	if output == nil {
		return
	}
	var entries []interface{}
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
