package tool

import "testing"

func TestServerFromPattern(t *testing.T) {
	testCases := []struct {
		pattern  string
		expected string
	}{
		{pattern: "analyst:ResourceTree", expected: "analyst"},
		{pattern: "analyst-ResourceTree", expected: "analyst"},
		{pattern: "llm/agents:list", expected: "llm/agents"},
		{pattern: "analyst-*", expected: "analyst"},
		{pattern: "analyst", expected: "analyst"},
	}

	for _, testCase := range testCases {
		if actual := serverFromPattern(testCase.pattern); actual != testCase.expected {
			t.Fatalf("serverFromPattern(%q) = %q, want %q", testCase.pattern, actual, testCase.expected)
		}
	}
}
