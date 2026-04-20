package skill

import "testing"

func TestParseAllowedTools(t *testing.T) {
	items := ParseAllowedTools("Bash(playwright-cli:*) Bash(npx:*) system/exec:execute system/os:*")
	if len(items) != 4 {
		t.Fatalf("len(items) = %d, want 4", len(items))
	}
	if items[0].BashCommand != "playwright-cli" {
		t.Fatalf("first bash command = %q", items[0].BashCommand)
	}
	if items[1].BashCommand != "npx" {
		t.Fatalf("second bash command = %q", items[1].BashCommand)
	}
	if items[2].ToolPattern != "system/exec:execute" {
		t.Fatalf("third tool pattern = %q", items[2].ToolPattern)
	}
	if items[3].ToolPattern != "system/os:*" {
		t.Fatalf("fourth tool pattern = %q", items[3].ToolPattern)
	}
}
