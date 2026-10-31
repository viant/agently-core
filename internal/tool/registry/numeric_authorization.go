package tool

import (
	"encoding/json"
	"strings"

	"github.com/viant/mcp-protocol/schema"
)

// Explicit authorization statuses outrank validation statuses when a producer
// supplies more than one error field. Text alone never creates numeric status.
func mcpErrorStatusPriority(status int) int {
	switch status {
	case 401:
		return 4
	case 403:
		return 3
	case 400:
		return 2
	case 422:
		return 1
	}
	return 0
}
func preferredMCPErrorStatus(current, candidate int) int {
	if mcpErrorStatusPriority(candidate) > mcpErrorStatusPriority(current) {
		return candidate
	}
	return current
}
func numericMCPErrorStatus(result *schema.CallToolResult) int {
	if result == nil || result.IsError == nil || !*result.IsError {
		return 0
	}
	status := numericMCPErrorPayload(result.StructuredContent)
	for _, content := range result.Content {
		raw, _ := json.Marshal(content)
		var block struct {
			Text string `json:"text"`
		}
		if json.Unmarshal(raw, &block) == nil {
			var value any
			if json.Unmarshal([]byte(block.Text), &value) == nil {
				status = preferredMCPErrorStatus(status, numericMCPErrorPayload(value))
			}
		}
	}
	return status
}
func numericMCPErrorPayload(value any) int {
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
				for _, candidate := range []int{400, 401, 403, 422} {
					if v == float64(candidate) {
						n = candidate
						break
					}
				}
			case json.Number:
				if parsed, e := v.Int64(); e == nil {
					for _, candidate := range []int{400, 401, 403, 422} {
						if parsed == int64(candidate) {
							n = candidate
							break
						}
					}
				}
			case map[string]any:
				n = numericMCPErrorPayload(v)
			}
			if n == 400 || n == 422 {
				_, nested := child.(map[string]any)
				if !nested && !strings.EqualFold(key, "status") && !strings.EqualFold(key, "statusCode") {
					n = 0 // An application code alone does not declare HTTP validation status.
				}
			}
			status = preferredMCPErrorStatus(status, n)
		}
	}
	return status
}

type upstreamMCPInputError struct {
	status  int
	cause   error
	message string
}

func newMCPInputError(result *schema.CallToolResult, status int, cause error) error {
	message := ""
	if payload, ok := result.StructuredContent.(map[string]any); ok && numericMCPErrorPayload(payload) == status {
		message, _ = payload["message"].(string)
	}
	if strings.TrimSpace(message) == "" {
		var payload struct {
			Message string `json:"message"`
		}
		if json.Unmarshal([]byte(cause.Error()), &payload) == nil {
			message = payload.Message
		}
	}
	return &upstreamMCPInputError{status: status, cause: cause, message: strings.TrimSpace(message)}
}
func (e *upstreamMCPInputError) Error() string {
	if e.message != "" {
		return e.message
	}
	return e.cause.Error()
}
func (e *upstreamMCPInputError) Unwrap() error       { return e.cause }
func (e *upstreamMCPInputError) HTTPStatusCode() int { return e.status }
