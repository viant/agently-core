package agent

import "testing"

func TestParseExplicitSkillInvocation(t *testing.T) {
	tests := []struct {
		input string
		name  string
		args  string
		ok    bool
	}{
		{input: "/playwright-cli https://example.com", name: "playwright-cli", args: "https://example.com", ok: true},
		{input: "   $playwright-cli run smoke", name: "playwright-cli", args: "run smoke", ok: true},
		{input: "/help", ok: false},
		{input: "please use /playwright-cli", ok: false},
	}
	for _, tc := range tests {
		name, args, ok := parseExplicitSkillInvocation(tc.input)
		if name != tc.name || args != tc.args || ok != tc.ok {
			t.Fatalf("parseExplicitSkillInvocation(%q) = (%q,%q,%v), want (%q,%q,%v)", tc.input, name, args, ok, tc.name, tc.args, tc.ok)
		}
	}
}
