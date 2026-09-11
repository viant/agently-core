package gemini

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/viant/agently-core/genai/llm/provider/base"
)

func TestWithStreamingDisabled(t *testing.T) {
	client := NewClient("test-key", "gemini-3.5-flash-lite", WithStreamingDisabled())
	if client.Implements(base.CanStream) {
		t.Fatal("expected streaming to be disabled")
	}
	if !client.Implements(base.CanUseTools) {
		t.Fatal("disabling streaming must not disable tools")
	}
}

func TestThinkingBudgetZeroIsSerialized(t *testing.T) {
	client := NewClient("test-key", "gemini-3.5-flash-lite", WithThinkingBudget(0))
	req := &Request{}
	client.applyThinkingDefault(req)
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	if !strings.Contains(string(data), `"thinkingBudget":0`) {
		t.Fatalf("request does not preserve explicit zero thinking budget: %s", data)
	}
}
