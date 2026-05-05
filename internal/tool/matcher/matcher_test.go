package matcher

import "testing"

func TestMatchSupportsAliasVariants(t *testing.T) {
	testCases := []struct {
		pattern string
		name    string
		want    bool
	}{
		{pattern: "analyst-ResourceTree", name: "analyst/ResourceTree", want: true},
		{pattern: "analyst:ResourceTree", name: "analyst/ResourceTree", want: true},
		{pattern: "analyst/ResourceTree", name: "analyst:ResourceTree", want: true},
		{pattern: "analyst-*", name: "analyst/ResourceTree", want: true},
	}

	for _, testCase := range testCases {
		if got := Match(testCase.pattern, testCase.name); got != testCase.want {
			t.Fatalf("Match(%q, %q) = %v, want %v", testCase.pattern, testCase.name, got, testCase.want)
		}
	}
}
