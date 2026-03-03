package openai

import "testing"

func TestBuildBackendWSURL(t *testing.T) {
	got, err := buildBackendWSURL("https://chatgpt.com/backend-api/codex")
	if err != nil {
		t.Fatalf("buildBackendWSURL error: %v", err)
	}
	want := "wss://chatgpt.com/backend-api/codex/responses"
	if got != want {
		t.Fatalf("unexpected ws url: got=%q want=%q", got, want)
	}
}

func TestHasInputPrefix(t *testing.T) {
	full := []InputItem{
		{Type: "message", Role: "user"},
		{Type: "message", Role: "assistant"},
	}
	prefix := []InputItem{
		{Type: "message", Role: "user"},
	}
	if !hasInputPrefix(full, prefix) {
		t.Fatalf("expected prefix match")
	}
	if hasInputPrefix(prefix, full) {
		t.Fatalf("expected non-prefix mismatch")
	}
}
