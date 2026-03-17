package debugtrace

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

const debugLogPath = "/tmp/agently-debug.log"

var (
	fileMu    sync.Mutex
	fileOnce  sync.Once
	debugFile *os.File
)

func ensureFile() *os.File {
	fileOnce.Do(func() {
		f, err := os.OpenFile(debugLogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			return
		}
		debugFile = f
	})
	return debugFile
}

// LogToFile writes a structured debug entry to /tmp/agently-debug.log.
// Safe to call concurrently. No-op if the file can't be opened.
func LogToFile(category, event string, data map[string]interface{}) {
	f := ensureFile()
	if f == nil {
		return
	}
	entry := map[string]interface{}{
		"ts":       time.Now().Format("15:04:05.000"),
		"category": category,
		"event":    event,
	}
	for k, v := range data {
		entry[k] = v
	}
	raw, err := json.Marshal(entry)
	if err != nil {
		return
	}
	fileMu.Lock()
	defer fileMu.Unlock()
	_, _ = f.Write(raw)
	_, _ = f.Write([]byte("\n"))
}

// LogLLMRequest logs a summary of the LLM request to the debug file.
func LogLLMRequest(model string, messageCount int, toolCount int, messages []map[string]string) {
	LogToFile("llm", "request", map[string]interface{}{
		"model":        model,
		"messageCount": messageCount,
		"toolCount":    toolCount,
		"messages":     messages,
	})
}

// LogLLMResponse logs a summary of the LLM response.
func LogLLMResponse(model string, content string, toolCalls []string, finishReason string) {
	LogToFile("llm", "response", map[string]interface{}{
		"model":        model,
		"contentLen":   len(content),
		"contentHead":  truncate(content, 200),
		"toolCalls":    toolCalls,
		"finishReason": finishReason,
	})
}

// LogToolCall logs a tool call execution result.
func LogToolCall(toolName, opID, status string, resultLen int, resultHead string, err string) {
	LogToFile("tool", "result", map[string]interface{}{
		"toolName":   toolName,
		"opID":       opID,
		"status":     status,
		"resultLen":  resultLen,
		"resultHead": truncate(resultHead, 500),
		"error":      err,
	})
}

// SummarizeMessagesForLog creates a compact summary of LLM messages.
func SummarizeMessagesForLog(messages interface{}) []map[string]string {
	type hasRoleContent interface {
		GetRole() string
		GetContent() string
	}
	// Use reflection-free approach: accept the json-marshalable messages
	raw, mErr := json.Marshal(messages)
	if mErr != nil {
		return nil
	}
	var parsed []map[string]interface{}
	if json.Unmarshal(raw, &parsed) != nil {
		return nil
	}
	out := make([]map[string]string, 0, len(parsed))
	for _, m := range parsed {
		role := fmt.Sprintf("%v", m["role"])
		content := fmt.Sprintf("%v", m["content"])
		toolCallID := ""
		if v, ok := m["tool_call_id"]; ok && v != nil {
			toolCallID = fmt.Sprintf("%v", v)
		}
		toolCalls := 0
		if v, ok := m["tool_calls"]; ok && v != nil {
			if arr, ok := v.([]interface{}); ok {
				toolCalls = len(arr)
			}
		}
		entry := map[string]string{
			"role":       role,
			"contentLen": fmt.Sprintf("%d", len(content)),
			"head":       truncate(content, 80),
		}
		if toolCallID != "" {
			entry["tool_call_id"] = toolCallID
		}
		if toolCalls > 0 {
			entry["tool_calls"] = fmt.Sprintf("%d", toolCalls)
		}
		out = append(out, entry)
	}
	return out
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
