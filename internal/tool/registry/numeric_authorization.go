package tool

import (
	"encoding/json"
	"github.com/viant/mcp-protocol/schema"
	"strings"
)

func numericMCPAuthorizationStatus(result *schema.CallToolResult) int {
	if result == nil || result.IsError == nil || !*result.IsError {
		return 0
	}
	status := numericAuthorizationPayload(result.StructuredContent)
	if status == 401 {
		return 401
	}
	for _, content := range result.Content {
		raw, _ := json.Marshal(content)
		var block struct {
			Text string `json:"text"`
		}
		if json.Unmarshal(raw, &block) == nil {
			var value any
			if json.Unmarshal([]byte(block.Text), &value) == nil {
				n := numericAuthorizationPayload(value)
				if n == 401 {
					return 401
				}
				if n == 403 {
					status = 403
				}
			}
		}
	}
	return status
}
func numericAuthorizationPayload(value any) int {
	object, ok := value.(map[string]any)
	if !ok {
		return 0
	}
	status := 0
	for key, child := range object {
		switch strings.ToLower(key) {
		case "code", "status", "statuscode", "error":
			n := 0
			switch v := child.(type) {
			case int:
				n = v
			case float64:
				if v == 401 || v == 403 {
					n = int(v)
				}
			case json.Number:
				if parsed, e := v.Int64(); e == nil && (parsed == 401 || parsed == 403) {
					n = int(parsed)
				}
			case map[string]any:
				n = numericAuthorizationPayload(v)
			}
			if n == 401 {
				return 401
			}
			if n == 403 {
				status = 403
			}
		}
	}
	return status
}
