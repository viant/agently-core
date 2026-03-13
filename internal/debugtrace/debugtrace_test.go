package debugtrace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trace.ndjson")
	t.Setenv(envTraceFile, path)

	Write("agent", "build_binding", map[string]any{"conversationID": "conv-1"})

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read trace: %v", err)
	}
	if len(data) == 0 {
		t.Fatalf("expected trace data")
	}
	var record Record
	if err := json.Unmarshal(data[:len(data)-1], &record); err != nil {
		t.Fatalf("unmarshal trace: %v", err)
	}
	if record.Component != "agent" {
		t.Fatalf("expected component agent, got %q", record.Component)
	}
	if record.Event != "build_binding" {
		t.Fatalf("expected event build_binding, got %q", record.Event)
	}
	if got := record.Fields["conversationID"]; got != "conv-1" {
		t.Fatalf("expected conversationID conv-1, got %#v", got)
	}
}

func TestWritePayload(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "payloads")
	t.Setenv(envPayloadDir, dir)

	path := WritePayload("llm-provider-request", "msg:1", []byte(`{"hello":"world"}`))
	if path == "" {
		t.Fatalf("expected payload path")
	}
	if filepath.Dir(path) != dir {
		t.Fatalf("expected dir %q, got %q", dir, filepath.Dir(path))
	}
	if filepath.Base(path) != "llm-provider-request-msg_1.json" {
		t.Fatalf("unexpected filename %q", filepath.Base(path))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read payload: %v", err)
	}
	if string(data) != `{"hello":"world"}` {
		t.Fatalf("unexpected payload body %q", string(data))
	}
}
