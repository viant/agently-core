package gemini

import (
	"strings"
	"testing"
)

func TestRedactAPIKey(t *testing.T) {
	const apiKey = "secret-api-key"
	got := redactAPIKey("request failed: https://example.test?key="+apiKey, apiKey)
	if strings.Contains(got, apiKey) {
		t.Fatalf("redacted error still contains API key: %q", got)
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Fatalf("redacted error does not contain marker: %q", got)
	}
}
