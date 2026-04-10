package modelcall

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/viant/agently-core/genai/llm"
)

const defaultToolResultPreviewLimit = 8 * 1024

// RedactGenerateRequestForTranscript returns a JSON snapshot of the request
// suitable for persisting into conversation transcripts.
//
// It removes large base64 payloads from message items (for example image/PDF
// attachments) while keeping enough metadata to understand that an attachment
// was present. This avoids exploding transcript size and log views.
func RedactGenerateRequestForTranscript(req *llm.GenerateRequest) []byte {
	if req == nil {
		return nil
	}
	clone := *req
	clone.Messages = make([]llm.Message, 0, len(req.Messages))

	for _, msg := range req.Messages {
		m := sanitizeToolReplayMessage(msg, toolResultPreviewLimit(req))
		if len(m.Items) > 0 {
			items := make([]llm.ContentItem, 0, len(m.Items))
			for _, item := range m.Items {
				redacted := item
				// Redact base64 payloads (binary content) from transcript snapshots.
				if redacted.Source == llm.SourceBase64 && strings.TrimSpace(redacted.Data) != "" {
					base64Len := len(redacted.Data)
					redacted.Data = ""
					if redacted.Metadata == nil {
						redacted.Metadata = map[string]interface{}{}
					}
					redacted.Metadata["dataBase64Omitted"] = true
					redacted.Metadata["base64Len"] = base64Len
				}
				items = append(items, redacted)
			}
			m.Items = items
		}
		if len(m.ContentItems) > 0 {
			items := make([]llm.ContentItem, 0, len(m.ContentItems))
			for _, item := range m.ContentItems {
				redacted := item
				if redacted.Source == llm.SourceBase64 && strings.TrimSpace(redacted.Data) != "" {
					base64Len := len(redacted.Data)
					redacted.Data = ""
					if redacted.Metadata == nil {
						redacted.Metadata = map[string]interface{}{}
					}
					redacted.Metadata["dataBase64Omitted"] = true
					redacted.Metadata["base64Len"] = base64Len
				}
				items = append(items, redacted)
			}
			m.ContentItems = items
		}
		clone.Messages = append(clone.Messages, m)
	}

	b, err := json.Marshal(&clone)
	if err != nil {
		return nil
	}
	return b
}

func toolResultPreviewLimit(request *llm.GenerateRequest) int {
	if request != nil && request.Options != nil && request.Options.Metadata != nil {
		switch actual := request.Options.Metadata["toolResultPreviewLimit"].(type) {
		case int:
			if actual > 0 {
				return actual
			}
		case int64:
			if actual > 0 {
				return int(actual)
			}
		case float64:
			if actual > 0 {
				return int(actual)
			}
		case string:
			actual = strings.TrimSpace(actual)
			if actual != "" {
				if parsed, err := json.Number(actual).Int64(); err == nil && parsed > 0 {
					return int(parsed)
				}
			}
		}
	}
	return defaultToolResultPreviewLimit
}

func sanitizeToolReplayMessage(msg llm.Message, threshold int) llm.Message {
	if threshold <= 0 {
		return msg
	}
	if msg.Role == llm.RoleTool {
		if body := strings.TrimSpace(msg.Content); body != "" {
			msg.Content = buildToolResultPreview(body, msg.ToolCallId, threshold)
		}
		if len(msg.Items) > 0 {
			items := make([]llm.ContentItem, len(msg.Items))
			copy(items, msg.Items)
			for i := range items {
				if items[i].Type != llm.ContentTypeText {
					continue
				}
				text := strings.TrimSpace(items[i].Data)
				if text == "" {
					text = strings.TrimSpace(items[i].Text)
				}
				if text == "" {
					continue
				}
				preview := buildToolResultPreview(text, msg.ToolCallId, threshold)
				items[i].Data = preview
				items[i].Text = preview
				break
			}
			msg.Items = items
		}
	}
	if len(msg.ToolCalls) > 0 {
		calls := make([]llm.ToolCall, len(msg.ToolCalls))
		copy(calls, msg.ToolCalls)
		for i := range calls {
			if body := strings.TrimSpace(calls[i].Result); body != "" {
				calls[i].Result = buildToolResultPreview(body, calls[i].ID, threshold)
			}
		}
		msg.ToolCalls = calls
	}
	return msg
}

func buildToolResultPreview(body, ref string, threshold int) string {
	body = strings.TrimSpace(body)
	if threshold <= 0 || len(body) <= threshold {
		return body
	}
	if jsonPreview, ok := truncateContinuationJSON(body, threshold); ok {
		return jsonPreview
	}
	limit := int(0.9 * float64(threshold))
	if limit <= 0 || limit > len(body) {
		limit = threshold
	}
	if limit > len(body) {
		limit = len(body)
	}
	returned := limit
	remaining := len(body) - returned
	chunk := middleTruncate(body, returned)
	chunk += fmt.Sprintf("[... omitted from %d to %d]", returned, len(body))
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return chunk
	}
	return fmt.Sprintf(`overflow: true
messageId: %s
nextRange:
  bytes:
    offset: %d
    length: %d
hasMore: true
remaining: %d
returned: %d
useToolToSeeMore: message-show
content: |
%s`, ref, returned, returned, remaining, returned, chunk)
}

func middleTruncate(body string, limit int) string {
	body = strings.TrimSpace(body)
	if limit <= 0 || len(body) <= limit {
		return body
	}
	head := limit / 2
	tail := limit - head
	if head <= 0 {
		head = limit
		tail = 0
	}
	start := strings.TrimSpace(body[:head])
	if tail <= 0 || tail >= len(body) {
		return start
	}
	end := strings.TrimSpace(body[len(body)-tail:])
	if end == "" {
		return start
	}
	return start + "\n[... omitted middle ...]\n" + end
}

func truncateContinuationJSON(body string, threshold int) (string, bool) {
	if !strings.HasPrefix(strings.TrimSpace(body), "{") {
		return "", false
	}
	var root map[string]interface{}
	if err := json.Unmarshal([]byte(body), &root); err != nil || root == nil {
		return "", false
	}
	cont, _ := root["continuation"].(map[string]interface{})
	if cont == nil {
		return "", false
	}
	hasMore, _ := cont["hasMore"].(bool)
	if !hasMore && intFromAny(cont["remaining"]) <= 0 {
		return "", false
	}
	if _, ok := cont["nextRange"].(map[string]interface{}); !ok {
		return "", false
	}
	parent, key, idx, value := findLargestString(root)
	if parent == nil || value == "" {
		out, err := json.Marshal(root)
		return string(out), err == nil
	}
	limit := int(0.9 * float64(threshold))
	if limit <= 0 || limit >= len(value) {
		limit = threshold
	}
	if limit <= 0 || limit >= len(value) {
		out, err := json.Marshal(root)
		return string(out), err == nil
	}
	truncated := middleTruncate(value, limit)
	switch container := parent.(type) {
	case map[string]interface{}:
		container[key] = truncated
	case []interface{}:
		if idx >= 0 && idx < len(container) {
			container[idx] = truncated
		}
	default:
		return "", false
	}
	out, err := json.Marshal(root)
	return string(out), err == nil
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
	return parent, key, idx, val
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
	}
	return 0
}
