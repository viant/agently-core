package overflow

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildJSONContinuationPreview_PreservesOriginalMessageID(t *testing.T) {
	body := `{"messageId":"source-msg","content":"` + strings.Repeat("A", 5000) + `"}`
	preview, ok := BuildJSONContinuationPreview(body, "tool-msg", 1000)
	if !ok {
		t.Fatalf("expected JSON continuation preview")
	}
	var root map[string]interface{}
	if err := json.Unmarshal([]byte(preview), &root); err != nil {
		t.Fatalf("failed to decode preview: %v\n%s", err, preview)
	}
	if got := stringField(root, "messageId"); got != "source-msg" {
		t.Fatalf("expected original messageId source-msg, got %q", got)
	}
	nextArgs := mapField(root, "nextArgs")
	if got := stringField(nextArgs, "messageId"); got != "source-msg" {
		t.Fatalf("expected nextArgs.messageId source-msg, got %q", got)
	}
	byteRange := mapField(nextArgs, "byteRange")
	if from := intField(byteRange, "from"); from <= 0 {
		t.Fatalf("expected positive byteRange.from, got %d", from)
	}
	if to := intField(byteRange, "to"); to <= intField(byteRange, "from") {
		t.Fatalf("expected byteRange.to > from, got from=%d to=%d", intField(byteRange, "from"), to)
	}
}

func TestBuildJSONContinuationPreview_PreservesNativeContinuationRange(t *testing.T) {
	body := `{"messageId":"source-msg","content":"` + strings.Repeat("A", 5000) + `","continuation":{"hasMore":true,"remaining":2100,"returned":900,"nextRange":{"bytes":{"offset":3600,"length":900}}}}`
	preview, ok := BuildJSONContinuationPreview(body, "tool-msg", 1000)
	if !ok {
		t.Fatalf("expected JSON continuation preview")
	}
	var root map[string]interface{}
	if err := json.Unmarshal([]byte(preview), &root); err != nil {
		t.Fatalf("failed to decode preview: %v\n%s", err, preview)
	}
	nextArgs := mapField(root, "nextArgs")
	byteRange := mapField(nextArgs, "byteRange")
	if from := intField(byteRange, "from"); from != 3600 {
		t.Fatalf("expected byteRange.from 3600, got %d", from)
	}
	if to := intField(byteRange, "to"); to != 4500 {
		t.Fatalf("expected byteRange.to 4500, got %d", to)
	}
}

func mapField(root map[string]interface{}, key string) map[string]interface{} {
	if root == nil {
		return nil
	}
	out, _ := root[key].(map[string]interface{})
	return out
}

func stringField(root map[string]interface{}, key string) string {
	if root == nil {
		return ""
	}
	value, _ := root[key].(string)
	return value
}

func intField(root map[string]interface{}, key string) int {
	if root == nil {
		return 0
	}
	switch actual := root[key].(type) {
	case float64:
		return int(actual)
	case int:
		return actual
	default:
		return 0
	}
}
