package data

import (
	"bytes"
	"context"
	"log"
	"strings"
	"testing"

	"github.com/viant/agently-core/internal/store/maintenancediag"
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
	t.Setenv(maintenancediag.DetailsEnv, "")
	ctx := context.Background()
	gotCtx, diagnostics := beginConversationDeleteDiagnostics(ctx, []string{"conversation-1"})
	if diagnostics != nil {
		t.Fatal("expected diagnostics to be disabled")
	}
	if gotCtx != ctx {
		t.Fatal("disabled diagnostics should not wrap the context")
	}
}

func TestBaseConversationDeleteDiagnosticsKeepsCompactRequestLogs(t *testing.T) {
	t.Setenv(conversationDeleteDiagnosticsEnv, "1")
	t.Setenv(maintenancediag.DetailsEnv, "0")
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	t.Cleanup(func() { log.SetOutput(previous) })
	ctx, diagnostics := beginConversationDeleteDiagnostics(context.Background(), []string{"conversation-1"})
	if diagnostics == nil || diagnostics.trace != nil || conversationDeleteDiagnosticsFromContext(ctx) != diagnostics {
		t.Fatal("base diagnostics should exist without a detailed trace")
	}
	diagnostics.finish(nil)
	logs := output.String()
	if !strings.Contains(logs, "phase=request event=start roots=\"conversation-1\"") ||
		!strings.Contains(logs, "phase=request event=done") ||
		strings.Contains(logs, "phase=component") || strings.Contains(logs, "phase=roots") {
		t.Fatalf("unexpected compact diagnostics: %s", logs)
	}
}

func TestDetailedConversationDeleteDiagnosticsSharesTrace(t *testing.T) {
	t.Setenv(conversationDeleteDiagnosticsEnv, "1")
	t.Setenv(maintenancediag.DetailsEnv, "1")
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	t.Cleanup(func() { log.SetOutput(previous) })
	ctx, diagnostics := beginConversationDeleteDiagnostics(context.Background(), []string{"conversation-1"})
	if diagnostics == nil || diagnostics.trace == nil || diagnostics.id != maintenancediag.ID(ctx) {
		t.Fatal("detailed diagnostics did not share the maintenance trace")
	}
	diagnostics.finish(nil)
	logs := output.String()
	if !strings.Contains(logs, "phase=request event=start operation=manual_conversation_delete") ||
		!strings.Contains(logs, "phase=roots") ||
		!strings.Contains(logs, "phase=request event=done") {
		t.Fatalf("unexpected detailed diagnostics: %s", logs)
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
