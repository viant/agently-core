package tool

import (
	"context"
	"testing"
)

func TestNormalizeMode(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"", ModeAuto},
		{"best", ModeBestPath},
		{"best-path", ModeBestPath},
		{"bestpath", ModeBestPath},
		{"AUTO", ModeAuto},
		{"ask", ModeAsk},
	}
	for _, tc := range tests {
		if got := NormalizeMode(tc.in); got != tc.want {
			t.Fatalf("NormalizeMode(%q): got %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestBestPathAllowed(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"message:remove", false},
		{"internal/message:remove", true},
		{"system/os:getEnv", true},
		{"system/exec:run", false},
		{"system/exec:execute", false},
		{"system/patch:apply", false},
	}
	for _, tc := range cases {
		if got := BestPathAllowed(tc.name); got != tc.want {
			t.Fatalf("BestPathAllowed(%q): got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestValidateExecution(t *testing.T) {
	p := &Policy{Mode: ModeBestPath}
	if err := ValidateExecution(context.Background(), p, "system/os:getEnv", map[string]interface{}{"names": []string{"USER"}}); err != nil {
		t.Fatalf("expected safe tool allowed, got: %v", err)
	}
	if err := ValidateExecution(context.Background(), p, "system/exec:execute", map[string]interface{}{"commands": []string{"date"}}); err == nil {
		t.Fatalf("expected risky tool to be blocked")
	}
}
