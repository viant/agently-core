package data

import (
	"context"
	"strings"
	"testing"
)

func TestConversationDeleteDiagnosticsEnabled(t *testing.T) {
	tests := []struct {
		value string
		want  bool
	}{
		{value: "", want: false},
		{value: "0", want: false},
		{value: "false", want: false},
		{value: "unexpected", want: false},
		{value: "1", want: true},
		{value: "true", want: true},
		{value: "YES", want: true},
		{value: " on ", want: true},
	}
	for _, test := range tests {
		t.Run(test.value, func(t *testing.T) {
			t.Setenv(conversationDeleteDiagnosticsEnv, test.value)
			if got := conversationDeleteDiagnosticsEnabled(); got != test.want {
				t.Fatalf("conversationDeleteDiagnosticsEnabled() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestBeginConversationDeleteDiagnosticsDisabledByDefault(t *testing.T) {
	t.Setenv(conversationDeleteDiagnosticsEnv, "")
	ctx := context.Background()
	gotCtx, diagnostics := beginConversationDeleteDiagnostics(ctx, []string{"conversation-1"})
	if diagnostics != nil {
		t.Fatal("expected diagnostics to be disabled")
	}
	if gotCtx != ctx {
		t.Fatal("disabled diagnostics should not wrap the context")
	}
}

func TestCompactConversationDeleteStatement(t *testing.T) {
	if got, want := compactConversationDeleteStatement("DELETE  FROM\n message\tWHERE id IN (?)"), "DELETE FROM message WHERE id IN (?)"; got != want {
		t.Fatalf("compactConversationDeleteStatement() = %q, want %q", got, want)
	}
	long := strings.Repeat("x", 300)
	if got := compactConversationDeleteStatement(long); len(got) != 240 || !strings.HasSuffix(got, "...") {
		t.Fatalf("compactConversationDeleteStatement() length/suffix = %d/%q", len(got), got[len(got)-3:])
	}
}
