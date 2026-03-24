package tool

import "testing"

func TestServerFromPattern(t *testing.T) {
	testCases := []struct {
		pattern  string
		expected string
	}{
		{pattern: "steward:AdHierarchy", expected: "steward"},
		{pattern: "steward-AdHierarchy", expected: "steward"},
		{pattern: "llm/agents:list", expected: "llm/agents"},
		{pattern: "steward-*", expected: "steward"},
		{pattern: "steward", expected: "steward"},
	}

	for _, testCase := range testCases {
		if actual := serverFromPattern(testCase.pattern); actual != testCase.expected {
			t.Fatalf("serverFromPattern(%q) = %q, want %q", testCase.pattern, actual, testCase.expected)
		}
	}
}
