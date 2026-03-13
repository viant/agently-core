package debugtrace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const envTraceFile = "AGENTLY_DEBUG_TRACE_FILE"
const envPayloadDir = "AGENTLY_DEBUG_PAYLOAD_DIR"

var mu sync.Mutex
var invalidFilenameChars = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

type Record struct {
	Time      string         `json:"time"`
	Component string         `json:"component"`
	Event     string         `json:"event"`
	PID       int            `json:"pid"`
	Fields    map[string]any `json:"fields,omitempty"`
}

func Enabled() bool {
	return strings.TrimSpace(os.Getenv(envTraceFile)) != ""
}

func Path() string {
	return strings.TrimSpace(os.Getenv(envTraceFile))
}

func PayloadDir() string {
	return strings.TrimSpace(os.Getenv(envPayloadDir))
}

func Write(component, event string, fields map[string]any) {
	path := Path()
	if path == "" {
		return
	}
	record := Record{
		Time:      time.Now().UTC().Format(time.RFC3339Nano),
		Component: strings.TrimSpace(component),
		Event:     strings.TrimSpace(event),
		PID:       os.Getpid(),
		Fields:    fields,
	}
	payload, err := json.Marshal(record)
	if err != nil {
		return
	}
	mu.Lock()
	defer mu.Unlock()
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		_ = os.MkdirAll(dir, 0o755)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(payload, '\n'))
}

func WritePayload(prefix, messageID string, body []byte) string {
	dir := PayloadDir()
	if dir == "" || len(body) == 0 {
		return ""
	}
	name := payloadFileName(prefix, messageID)
	path := filepath.Join(dir, name)
	mu.Lock()
	defer mu.Unlock()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ""
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		return ""
	}
	return path
}

func payloadFileName(prefix, messageID string) string {
	prefix = sanitizeFilePart(prefix)
	messageID = sanitizeFilePart(messageID)
	if prefix == "" {
		prefix = "payload"
	}
	if messageID == "" {
		messageID = "unknown"
	}
	return prefix + "-" + messageID + ".json"
}

func sanitizeFilePart(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return invalidFilenameChars.ReplaceAllString(value, "_")
}
