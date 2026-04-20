package async

import (
	"strings"
	"testing"
)

type testStringer string

func (t testStringer) String() string { return string(t) }

func TestExtractIntent(t *testing.T) {
	long := strings.Repeat("x", 220)
	tests := []struct {
		name     string
		args     map[string]any
		path     string
		fallback string
		want     string
	}{
		{
			name: "top level objective",
			args: map[string]any{"objective": " Inspect   the repository at /foo\nand summarize structure "},
			path: "objective", fallback: "tool:start",
			want: "Inspect the repository at /foo and summarize structure",
		},
		{
			name: "nested path",
			args: map[string]any{"context": map[string]any{"workdir": "/tmp/project"}},
			path: "context.workdir", fallback: "tool:start",
			want: "/tmp/project",
		},
		{
			name: "missing path falls back",
			args: map[string]any{"objective": "inspect"},
			path: "context.workdir", fallback: "tool:start",
			want: "tool:start",
		},
		{
			name: "empty path falls back",
			args: map[string]any{"objective": "inspect"},
			path: "", fallback: "tool:start",
			want: "tool:start",
		},
		{
			name: "stringer supported",
			args: map[string]any{"intent": testStringer("  hello   world  ")},
			path: "intent", fallback: "tool:start",
			want: "hello world",
		},
		{
			name: "containers become fallback",
			args: map[string]any{"intent": map[string]any{"nested": true}},
			path: "intent", fallback: "tool:start",
			want: "tool:start",
		},
		{
			name: "truncates to 200 runes",
			args: map[string]any{"intent": long},
			path: "intent", fallback: "tool:start",
			want: strings.Repeat("x", 200),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ExtractIntent(tc.args, tc.path, tc.fallback)
			if got != tc.want {
				t.Fatalf("ExtractIntent() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestExtractSummary(t *testing.T) {
	tests := []struct {
		name  string
		args  map[string]any
		paths []string
		want  string
	}{
		{
			name:  "summary paths preferred",
			args:  map[string]any{"objective": "Analyze order 2639076 performance", "orderId": 2639076, "context": map[string]any{"workdir": "/tmp/ws"}},
			paths: []string{"orderId", "context.workdir"},
			want:  "orderId=2639076 | workdir=/tmp/ws",
		},
		{
			name: "top level scalar fallback",
			args: map[string]any{
				"orderId":   2639076,
				"objective": "Analyze order 2639076 performance",
				"_agently":  map[string]any{"async": true},
			},
			want: "objective=Analyze order 2639076 performance | orderId=2639076",
		},
		{
			name: "containers ignored when no scalar summary",
			args: map[string]any{
				"context": map[string]any{"workdir": "/tmp/ws"},
				"items":   []any{"a", "b"},
			},
			want: "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ExtractSummary(tc.args, tc.paths)
			if got != tc.want {
				t.Fatalf("ExtractSummary() = %q, want %q", got, tc.want)
			}
		})
	}
}
