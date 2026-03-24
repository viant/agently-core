package matcher

import "testing"

func TestMatchSupportsAliasVariants(t *testing.T) {
	testCases := []struct {
		pattern string
		name    string
		want    bool
	}{
		{pattern: "steward-AdHierarchy", name: "steward/AdHierarchy", want: true},
		{pattern: "steward:AdHierarchy", name: "steward/AdHierarchy", want: true},
		{pattern: "steward/AdHierarchy", name: "steward:AdHierarchy", want: true},
		{pattern: "steward-*", name: "steward/AdHierarchy", want: true},
	}

	for _, testCase := range testCases {
		if got := Match(testCase.pattern, testCase.name); got != testCase.want {
			t.Fatalf("Match(%q, %q) = %v, want %v", testCase.pattern, testCase.name, got, testCase.want)
		}
	}
}
